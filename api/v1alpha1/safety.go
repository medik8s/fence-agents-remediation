package v1alpha1

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	commonannotations "github.com/medik8s/common/pkg/annotations"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8svalidation "k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/medik8s/fence-agents-remediation/v5/pkg/redfish"
)

const RedfishOffV1 = "RedfishOffV1"

// NodeBinding identifies the Node selected by NHC, independently of later name reuse.
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="node binding is immutable"
type NodeBinding struct {
	// +kubebuilder:validation:Enum=v1
	Version string `json:"version"`
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// +kubebuilder:validation:MinLength=1
	UID string `json:"uid"`
	// +kubebuilder:validation:Pattern="^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$"
	SystemUUID string `json:"systemUUID"`
}

// CredentialVersion invalidates evidence on Secret replacement or content updates.
type CredentialVersion struct {
	Namespace       string `json:"namespace"`
	Name            string `json:"name"`
	UID             string `json:"uid"`
	ResourceVersion string `json:"resourceVersion"`
}

// SafetyTarget contains no credential data or credential-derived digest.
type SafetyTarget struct {
	Endpoint    string            `json:"endpoint"`
	SystemURI   string            `json:"systemURI"`
	TrustDigest string            `json:"trustDigest"`
	Credential  CredentialVersion `json:"credential"`
}

// SafetySnapshot is persisted before action and must match every later effect.
type SafetySnapshot struct {
	FARUID     string       `json:"farUID"`
	Generation int64        `json:"generation"`
	SpecDigest string       `json:"specDigest"`
	Binding    NodeBinding  `json:"binding"`
	Target     SafetyTarget `json:"target"`
}

// OffProof binds a completed physical-state observation to its selected target.
type OffProof struct {
	Snapshot    SafetySnapshot `json:"snapshot"`
	ConfirmedAt metav1.Time    `json:"confirmedAt"`
}

// SafetyConfig is operator configuration, never supplied by a remediation CR.
// +kubebuilder:object:generate=false
type SafetyConfig struct {
	Enabled             bool
	TrustedNHCCreator   string
	CredentialNamespace string
	CA                  []byte
}

// ResolvedSafetyTarget keeps credentials in memory only.
// +kubebuilder:object:generate=false
type ResolvedSafetyTarget struct {
	Target   SafetyTarget
	Username string
	Password string
}

func (c SafetyConfig) Validate() error {
	parts := strings.Split(c.TrustedNHCCreator, ":")
	if !c.Enabled || c.CredentialNamespace == "" || len(parts) != 4 || parts[0] != "system" || parts[1] != "serviceaccount" || parts[2] == "" || parts[3] == "" || strings.Contains(c.TrustedNHCCreator, "*") {
		return fmt.Errorf("safety-mode requires a namespace and an explicit trusted NHC service account")
	}
	_, err := publicTrustDigest(c.CA)
	return err
}

func publicTrustDigest(ca []byte) (string, error) {
	var certificates []byte
	for rest := bytes.TrimSpace(ca); len(rest) > 0; rest = bytes.TrimSpace(rest) {
		if !bytes.HasPrefix(rest, []byte("-----BEGIN CERTIFICATE-----")) {
			return "", fmt.Errorf("redfish trust file must contain only public certificates")
		}
		block, remaining := pem.Decode(rest)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return "", fmt.Errorf("invalid public Redfish certificate")
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return "", fmt.Errorf("invalid public Redfish certificate")
		}
		certificates = append(certificates, certificate.Raw...)
		rest = remaining
	}
	if len(certificates) == 0 {
		return "", fmt.Errorf("safety-mode requires a trusted Redfish CA")
	}
	digest := sha256.Sum256(certificates)
	return fmt.Sprintf("%x", digest), nil
}

func (b NodeBinding) Validate() error {
	if b.Version != "v1" || b.Name == "" || b.UID == "" || !redfish.ValidSystemUUID(b.SystemUUID) || b.SystemUUID != strings.ToLower(b.SystemUUID) {
		return fmt.Errorf("invalid selected Node binding")
	}
	return nil
}

func (b NodeBinding) Matches(node *corev1.Node) error {
	if err := b.Validate(); err != nil {
		return err
	}
	if node == nil || node.Name != b.Name || string(node.UID) != b.UID || !redfish.ValidSystemUUID(node.Status.NodeInfo.SystemUUID) || !strings.EqualFold(node.Status.NodeInfo.SystemUUID, b.SystemUUID) {
		return fmt.Errorf("selected Node identity no longer matches")
	}
	return nil
}

// ReadUniqueBoundNode refuses ambiguity among known Nodes without treating names as identity.
func ReadUniqueBoundNode(ctx context.Context, reader client.Reader, binding NodeBinding) (*corev1.Node, error) {
	if err := binding.Validate(); err != nil {
		return nil, err
	}
	if reader == nil {
		return nil, fmt.Errorf("uncached Node reader required")
	}
	nodes := &corev1.NodeList{}
	if err := reader.List(ctx, nodes); err != nil {
		return nil, fmt.Errorf("list known Nodes for identity validation: %w", err)
	}
	if nodes.Continue != "" || (nodes.RemainingItemCount != nil && *nodes.RemainingItemCount > 0) {
		return nil, fmt.Errorf("incomplete known Node inventory cannot establish UUID uniqueness")
	}
	var selected *corev1.Node
	matches := 0
	for i := range nodes.Items {
		node := &nodes.Items[i]
		if strings.EqualFold(node.Status.NodeInfo.SystemUUID, binding.SystemUUID) {
			matches++
			selected = node
		}
	}
	if matches != 1 {
		return nil, fmt.Errorf("selected system UUID does not uniquely identify a known Node")
	}
	if err := binding.Matches(selected); err != nil {
		return nil, err
	}
	if !selected.DeletionTimestamp.IsZero() {
		return nil, fmt.Errorf("selected Node is terminating")
	}
	return selected.DeepCopy(), nil
}

// ValidateSafetyFAR also checks the legacy routing hint, without using it as identity.
func ValidateSafetyFAR(far *FenceAgentsRemediation) error {
	if err := ValidateSafetySpec(&far.Spec, false); err != nil {
		return err
	}
	if hint, found := far.Annotations[commonannotations.NodeNameAnnotation]; found && hint != far.Spec.NodeBinding.Name {
		return fmt.Errorf("node-name hint differs from selected binding")
	}
	return nil
}

// ValidateSafetySpec permits templates without a binding, but never a supplied template identity.
func ValidateSafetySpec(spec *FenceAgentsRemediationSpec, isTemplate bool) error {
	if spec.SafetyPolicy != RedfishOffV1 || spec.Agent != "fence_redfish" || spec.RemediationStrategy != OutOfServiceTaintRemediationStrategy {
		return fmt.Errorf("safety-mode supports only RedfishOffV1 with fence_redfish and OutOfServiceTaint")
	}
	if spec.RetryCount < 1 || spec.RetryCount > 5 || spec.Timeout.Duration < time.Second || spec.Timeout.Duration > time.Minute || spec.RetryInterval.Duration < time.Second || spec.RetryInterval.Duration > 30*time.Second {
		return fmt.Errorf("safety-mode retry or timeout exceeds bounds")
	}
	if isTemplate {
		if spec.NodeBinding != nil {
			return fmt.Errorf("templates must not supply a Node binding")
		}
	} else {
		if spec.NodeBinding == nil {
			return fmt.Errorf("selected Node binding required")
		}
		if err := spec.NodeBinding.Validate(); err != nil {
			return err
		}
	}
	if spec.SharedSecretName != nil && len(k8svalidation.IsDNS1123Subdomain(*spec.SharedSecretName)) != 0 {
		return fmt.Errorf("invalid shared credential Secret reference")
	}
	for _, name := range spec.NodeSecretNames {
		if len(k8svalidation.IsDNS1123Subdomain(name)) != 0 {
			return fmt.Errorf("invalid node credential Secret reference")
		}
	}
	validate := func(key ParameterName, value string) error {
		switch key {
		case "--ip", "--ipport", "--systems-uri", "--action":
		default:
			return fmt.Errorf("unsupported safety-mode parameter")
		}
		if value == "" || strings.Contains(value, "{{") || strings.ContainsAny(value, "\r\n\x00") {
			return fmt.Errorf("invalid safety-mode parameter")
		}
		if key == "--action" && value != "off" {
			return fmt.Errorf("safety-mode requires explicit off action")
		}
		return nil
	}
	for key, value := range spec.SharedParameters {
		if err := validate(key, value); err != nil {
			return err
		}
	}
	for key, nodes := range spec.NodeParameters {
		if len(nodes) == 0 {
			return fmt.Errorf("empty safety-mode node parameter map")
		}
		for _, value := range nodes {
			if err := validate(key, value); err != nil {
				return err
			}
		}
	}
	return nil
}

// ResolveSafetyTarget reads only the configured credential namespace, never arbitrary CLI options.
func ResolveSafetyTarget(ctx context.Context, reader client.Reader, far *FenceAgentsRemediation, cfg SafetyConfig) (*ResolvedSafetyTarget, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if err := ValidateSafetyFAR(far); err != nil {
		return nil, err
	}
	if far.Namespace != cfg.CredentialNamespace || reader == nil {
		return nil, fmt.Errorf("remediation must use the configured BMC namespace")
	}
	params := map[ParameterName]string{}
	for key, value := range far.Spec.SharedParameters {
		params[key] = value
	}
	for key, nodes := range far.Spec.NodeParameters {
		if value, found := nodes[NodeName(far.Spec.NodeBinding.Name)]; found {
			params[key] = value
		}
	}
	if params["--action"] != "off" {
		return nil, fmt.Errorf("explicit off action required")
	}
	host := params["--ip"]
	if host == "" || strings.ContainsAny(host, "/@?#%\\") {
		return nil, fmt.Errorf("invalid Redfish host")
	}
	port := params["--ipport"]
	if port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return nil, fmt.Errorf("invalid Redfish port")
		}
		host = net.JoinHostPort(host, strconv.Itoa(value))
	} else if strings.Contains(host, ":") {
		if net.ParseIP(host) == nil {
			return nil, fmt.Errorf("redfish port must be explicit")
		}
		host = "[" + host + "]"
	}
	endpoint := (&url.URL{Scheme: "https", Host: strings.ToLower(host)}).String()
	uri := params["--systems-uri"]
	if err := redfish.ValidateTarget(endpoint, uri); err != nil {
		return nil, err
	}
	secretName := ""
	if far.Spec.SharedSecretName != nil {
		secretName = *far.Spec.SharedSecretName
	}
	if name, found := far.Spec.NodeSecretNames[NodeName(far.Spec.NodeBinding.Name)]; found {
		if secretName != "" {
			return nil, fmt.Errorf("one credential Secret per selected target is required")
		}
		secretName = name
	}
	if secretName == "" {
		return nil, fmt.Errorf("credential Secret required")
	}
	secret := &corev1.Secret{}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: cfg.CredentialNamespace, Name: secretName}, secret); err != nil {
		return nil, fmt.Errorf("read selected BMC credentials: %w", err)
	}
	if len(secret.Data) != 2 || len(secret.Data["username"]) == 0 || len(secret.Data["password"]) == 0 || secret.UID == "" || secret.ResourceVersion == "" {
		return nil, fmt.Errorf("credential Secret requires only nonempty username and password and a persisted version")
	}
	digest, err := publicTrustDigest(cfg.CA)
	if err != nil {
		return nil, err
	}
	return &ResolvedSafetyTarget{Target: SafetyTarget{Endpoint: endpoint, SystemURI: uri, TrustDigest: digest, Credential: CredentialVersion{Namespace: cfg.CredentialNamespace, Name: secret.Name, UID: string(secret.UID), ResourceVersion: secret.ResourceVersion}}, Username: string(secret.Data["username"]), Password: string(secret.Data["password"])}, nil
}

func NewSafetySnapshot(far *FenceAgentsRemediation, target SafetyTarget) (SafetySnapshot, error) {
	if err := ValidateSafetyFAR(far); err != nil {
		return SafetySnapshot{}, err
	}
	data, err := json.Marshal(far.Spec)
	if err != nil {
		return SafetySnapshot{}, fmt.Errorf("encode safety spec: %w", err)
	}
	digest := sha256.Sum256(data)
	return SafetySnapshot{FARUID: string(far.UID), Generation: far.Generation, SpecDigest: fmt.Sprintf("%x", digest), Binding: *far.Spec.NodeBinding, Target: target}, nil
}
