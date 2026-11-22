package safetycontract

import (
	"context"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	commonannotations "github.com/medik8s/common/pkg/annotations"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	admissionv1 "k8s.io/api/admission/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	farapi "github.com/medik8s/fence-agents-remediation/v5/api/v1alpha1"
)

const systemUUID = "00112233-4455-4677-8899-aabbccddeeff"

func safetyFixture(t testing.TB) (*farapi.FenceAgentsRemediation, farapi.SafetyConfig, *corev1.Secret) {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(server.Close)
	name := "credentials"
	return &farapi.FenceAgentsRemediation{ObjectMeta: metav1.ObjectMeta{Name: "worker-a", Namespace: "bmc", UID: "far-a", Generation: 1, Annotations: identityAnnotations("worker-a", "node-a", systemUUID)}, Spec: farapi.FenceAgentsRemediationSpec{
		SafetyPolicy: farapi.RedfishOffV1,
		Agent:        "fence_redfish", RetryCount: 1, RetryInterval: metav1.Duration{Duration: time.Second}, Timeout: metav1.Duration{Duration: time.Second}, RemediationStrategy: farapi.OutOfServiceTaintRemediationStrategy,
		SharedParameters: map[farapi.ParameterName]string{"--action": "off", "--ip": "bmc.example", "--systems-uri": "/redfish/v1/Systems/1"}, SharedSecretName: &name,
	}}, farapi.SafetyConfig{Enabled: true, CredentialNamespace: "bmc", TrustedNHCCreator: "system:serviceaccount:operators:nhc", CA: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})}, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "bmc", UID: "secret-a", ResourceVersion: "1"}, Data: map[string][]byte{"username": []byte("user"), "password": []byte("password")}}
}

func identityAnnotations(name, uid, uuid string) map[string]string {
	return map[string]string{commonannotations.NodeNameAnnotation: name, commonannotations.NodeUIDAnnotation: uid, commonannotations.NodeSystemUUIDAnnotation: uuid}
}

var _ = Describe("Safety-mode API validation", func() {
	for _, tc := range []struct {
		name    string
		mutate  func(*farapi.FenceAgentsRemediation)
		wantErr bool
	}{
		{name: "valid", mutate: func(*farapi.FenceAgentsRemediation) {}},
		{name: "missing node name", mutate: func(f *farapi.FenceAgentsRemediation) { delete(f.Annotations, commonannotations.NodeNameAnnotation) }, wantErr: true},
		{name: "missing node UID", mutate: func(f *farapi.FenceAgentsRemediation) { delete(f.Annotations, commonannotations.NodeUIDAnnotation) }, wantErr: true},
		{name: "missing system UUID", mutate: func(f *farapi.FenceAgentsRemediation) {
			delete(f.Annotations, commonannotations.NodeSystemUUIDAnnotation)
		}, wantErr: true},
		{name: "malformed system UUID", mutate: func(f *farapi.FenceAgentsRemediation) {
			f.Annotations[commonannotations.NodeSystemUUIDAnnotation] = "not-a-uuid"
		}, wantErr: true},
		{name: "all-zero system UUID", mutate: func(f *farapi.FenceAgentsRemediation) {
			f.Annotations[commonannotations.NodeSystemUUIDAnnotation] = "00000000-0000-0000-0000-000000000000"
		}, wantErr: true},
		{name: "uppercase system UUID as the Node reports it", mutate: func(f *farapi.FenceAgentsRemediation) {
			f.Annotations[commonannotations.NodeSystemUUIDAnnotation] = strings.ToUpper(systemUUID)
		}},
		{name: "unknown policy", mutate: func(f *farapi.FenceAgentsRemediation) { f.Spec.SafetyPolicy = "unknown" }, wantErr: true},
		{name: "retry zero", mutate: func(f *farapi.FenceAgentsRemediation) { f.Spec.RetryCount = 0 }, wantErr: true},
		{name: "unbounded retries", mutate: func(f *farapi.FenceAgentsRemediation) { f.Spec.RetryCount = 100 }, wantErr: true},
		{name: "unbounded timeout", mutate: func(f *farapi.FenceAgentsRemediation) { f.Spec.Timeout.Duration = time.Hour }, wantErr: true},
		{name: "pod deletion", mutate: func(f *farapi.FenceAgentsRemediation) {
			f.Spec.RemediationStrategy = farapi.ResourceDeletionRemediationStrategy
		}, wantErr: true},
		{name: "IPMI", mutate: func(f *farapi.FenceAgentsRemediation) { f.Spec.Agent = "fence_ipmilan" }, wantErr: true},
		{name: "reboot", mutate: func(f *farapi.FenceAgentsRemediation) { f.Spec.SharedParameters["--action"] = "reboot" }, wantErr: true},
		{name: "inline password", mutate: func(f *farapi.FenceAgentsRemediation) { f.Spec.SharedParameters["--password"] = "password" }, wantErr: true},
		{name: "insecure", mutate: func(f *farapi.FenceAgentsRemediation) { f.Spec.SharedParameters["--ssl-insecure"] = "" }, wantErr: true},
		{name: "help", mutate: func(f *farapi.FenceAgentsRemediation) { f.Spec.SharedParameters["--help"] = "" }, wantErr: true},
		{name: "empty node option map", mutate: func(f *farapi.FenceAgentsRemediation) {
			f.Spec.NodeParameters = map[farapi.ParameterName]map[farapi.NodeName]string{"--help": {}}
		}, wantErr: true},
	} {
		It(tc.name, func() {
			t := GinkgoTB()
			f, _, _ := safetyFixture(t)
			tc.mutate(f)
			if err := farapi.ValidateSafetyFAR(f); (err != nil) != tc.wantErr {
				t.Errorf("error=%v wantErr=%t", err, tc.wantErr)
			}
		})
	}
})

var _ = Describe("Safety-mode Secrets and targets", func() {
	for _, tc := range []struct {
		name    string
		mutate  func(*farapi.FenceAgentsRemediation, *farapi.SafetyConfig, *corev1.Secret)
		wantErr bool
	}{
		{name: "valid", mutate: func(*farapi.FenceAgentsRemediation, *farapi.SafetyConfig, *corev1.Secret) {}},
		{name: "secret option", mutate: func(_ *farapi.FenceAgentsRemediation, _ *farapi.SafetyConfig, s *corev1.Secret) {
			s.Data["--help"] = []byte{}
		}, wantErr: true},
		{name: "missing password", mutate: func(_ *farapi.FenceAgentsRemediation, _ *farapi.SafetyConfig, s *corev1.Secret) {
			delete(s.Data, "password")
		}, wantErr: true},
		{name: "foreign namespace", mutate: func(f *farapi.FenceAgentsRemediation, _ *farapi.SafetyConfig, _ *corev1.Secret) {
			f.Namespace = "foreign"
		}, wantErr: true},
		{name: "empty shared reference with node reference", mutate: func(f *farapi.FenceAgentsRemediation, _ *farapi.SafetyConfig, s *corev1.Secret) {
			empty := ""
			f.Spec.SharedSecretName = &empty
			f.Spec.NodeSecretNames = map[farapi.NodeName]string{"worker-a": s.Name}
		}, wantErr: true},
		{name: "mixed certificate and private key", mutate: func(_ *farapi.FenceAgentsRemediation, c *farapi.SafetyConfig, _ *corev1.Secret) {
			c.CA = append(c.CA, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("synthetic-key")})...)
		}, wantErr: true},
		{name: "missing CA", mutate: func(_ *farapi.FenceAgentsRemediation, c *farapi.SafetyConfig, _ *corev1.Secret) { c.CA = nil }, wantErr: true},
		{name: "missing creator", mutate: func(_ *farapi.FenceAgentsRemediation, c *farapi.SafetyConfig, _ *corev1.Secret) {
			c.TrustedNHCCreator = ""
		}, wantErr: true},
		{name: "URI discovery", mutate: func(f *farapi.FenceAgentsRemediation, _ *farapi.SafetyConfig, _ *corev1.Secret) {
			delete(f.Spec.SharedParameters, "--systems-uri")
		}, wantErr: true},
		{name: "URI query", mutate: func(f *farapi.FenceAgentsRemediation, _ *farapi.SafetyConfig, _ *corev1.Secret) {
			f.Spec.SharedParameters["--systems-uri"] = "/redfish/v1/Systems/1?target=2"
		}, wantErr: true},
		{name: "endpoint userinfo", mutate: func(f *farapi.FenceAgentsRemediation, _ *farapi.SafetyConfig, _ *corev1.Secret) {
			f.Spec.SharedParameters["--ip"] = "user@bmc.example"
		}, wantErr: true},
	} {
		It(tc.name, func() {
			t := GinkgoTB()
			f, config, secret := safetyFixture(t)
			tc.mutate(f, &config, secret)
			scheme := runtime.NewScheme()
			if err := corev1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()
			resolved, err := farapi.ResolveSafetyTarget(context.Background(), reader, f, config)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error=%v wantErr=%t", err, tc.wantErr)
			}
			if err == nil && (resolved.Target.Credential.UID != string(secret.UID) || resolved.Target.Credential.ResourceVersion != secret.ResourceVersion || resolved.Username != "user") {
				t.Fatal("target lost credential version")
			}
		})
	}
})

var _ = It("includes Node identity in the snapshot digest", func() {
	t := GinkgoTB()
	far, _, _ := safetyFixture(t)
	target := farapi.SafetyTarget{Endpoint: "https://bmc.example", SystemURI: "/redfish/v1/Systems/1"}
	original, err := farapi.NewSafetySnapshot(far, target)
	if err != nil {
		t.Fatal(err)
	}
	if original.Binding.Name != "worker-a" || original.Binding.UID != "node-a" || original.Binding.SystemUUID != systemUUID {
		t.Fatalf("snapshot binding = %+v, want the identity annotations", original.Binding)
	}
	changed := far.DeepCopy()
	changed.Annotations[commonannotations.NodeUIDAnnotation] = "node-b"
	replaced, err := farapi.NewSafetySnapshot(changed, target)
	if err != nil {
		t.Fatal(err)
	}
	if replaced.SpecDigest == original.SpecDigest {
		t.Error("snapshot digest unchanged after the node UID annotation changed")
	}
})

var _ = Describe("Safety-mode admission", func() {
	for _, tc := range []struct {
		name                                                              string
		untrusted, missingConfig, replacement, mutateSpec, mutateIdentity bool
		mutateOtherMetadata                                               bool
		wantErr                                                           bool
		duplicate                                                         bool
	}{
		{name: "trusted NHC create"},
		{name: "duplicate known UUID", duplicate: true, wantErr: true},
		{name: "manual CR denied", untrusted: true, wantErr: true},
		{name: "missing creator config", missingConfig: true, wantErr: true},
		{name: "replacement UID", replacement: true, wantErr: true},
		{name: "immutable effective spec", mutateSpec: true, wantErr: true},
		{name: "immutable identity annotations", mutateIdentity: true, wantErr: true},
		{name: "other metadata stays mutable", mutateOtherMetadata: true},
	} {
		It(tc.name, func() {
			t := GinkgoTB()
			far, config, secret := safetyFixture(t)
			node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: far.Annotations[commonannotations.NodeNameAnnotation], UID: "node-a"}, Status: corev1.NodeStatus{NodeInfo: corev1.NodeSystemInfo{SystemUUID: systemUUID}}}
			if tc.replacement {
				node.UID = "node-b"
			}
			if tc.missingConfig {
				config.TrustedNHCCreator = ""
			}
			scheme := runtime.NewScheme()
			if err := corev1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(node, secret).Build()
			if tc.duplicate {
				other := node.DeepCopy()
				other.Name = "worker-b"
				other.UID = "node-b"
				other.ResourceVersion = ""
				if err := c.Create(context.Background(), other); err != nil {
					t.Fatal(err)
				}
			}
			validator := &farapi.FARValidator{Client: c, APIReader: c, Safety: config}
			username := config.TrustedNHCCreator
			if tc.untrusted {
				username = "system:serviceaccount:other:manual"
			}
			ctx := admission.NewContextWithRequest(context.Background(), admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{UserInfo: authenticationv1.UserInfo{Username: username}, Namespace: far.Namespace}})
			var err error
			switch {
			case tc.mutateIdentity:
				changed := far.DeepCopy()
				changed.Annotations[commonannotations.NodeUIDAnnotation] = "node-b"
				_, err = validator.ValidateUpdate(ctx, far, changed)
			case tc.mutateOtherMetadata:
				changed := far.DeepCopy()
				changed.Annotations[commonannotations.NhcTimedOut] = "now"
				changed.Finalizers = []string{farapi.FARFinalizer}
				_, err = validator.ValidateUpdate(ctx, far, changed)
			case tc.mutateSpec:
				changed := far.DeepCopy()
				changed.Spec.SharedParameters = map[farapi.ParameterName]string{"--ip": "other.example"}
				_, err = validator.ValidateUpdate(ctx, far, changed)
			default:
				_, err = validator.ValidateCreate(ctx, far)
			}
			if (err != nil) != tc.wantErr {
				t.Errorf("error=%v wantErr=%t", err, tc.wantErr)
			}
		})
	}
})

var _ = Describe("Safety-mode finalizer admission", func() {
	for _, tc := range []struct {
		name                          string
		legacy, terminating, disabled bool
		changeIdentity                bool
		removeIdentity, addIdentity   bool
		changeSpec                    bool
		wantErr                       bool
	}{
		{name: "legacy remediation while safety-mode is on", legacy: true},
		{name: "terminating remediation while safety-mode is off", terminating: true, disabled: true},
		{name: "terminating remediation keeps its identity", terminating: true, disabled: true, changeIdentity: true, wantErr: true},
		{name: "live safety remediation while safety-mode is off", disabled: true, wantErr: true},
		{name: "terminating remediation cannot drop an identity annotation", terminating: true, disabled: true, removeIdentity: true, wantErr: true},
		{name: "legacy remediation cannot gain an identity annotation", legacy: true, addIdentity: true, wantErr: true},
		{name: "legacy remediation spec stays immutable", legacy: true, changeSpec: true, wantErr: true},
	} {
		It(tc.name, func() {
			t := GinkgoTB()
			far, config, _ := safetyFixture(t)
			far.Finalizers = []string{farapi.FARFinalizer}
			if tc.legacy {
				far.Spec.SafetyPolicy = ""
			}
			if tc.terminating {
				now := metav1.Now()
				far.DeletionTimestamp = &now
			}
			if tc.disabled {
				config.Enabled = false
			}
			if tc.addIdentity {
				delete(far.Annotations, commonannotations.NodeUIDAnnotation)
			}
			changed := far.DeepCopy()
			changed.Finalizers = nil
			if tc.removeIdentity {
				delete(changed.Annotations, commonannotations.NodeSystemUUIDAnnotation)
			}
			if tc.addIdentity {
				changed.Annotations[commonannotations.NodeUIDAnnotation] = "node-a"
			}
			if tc.changeSpec {
				changed.Spec.SharedParameters["--ip"] = "other.example"
			}
			if tc.changeIdentity {
				changed.Annotations[commonannotations.NodeUIDAnnotation] = "node-b"
			}
			_, err := (&farapi.FARValidator{Safety: config}).ValidateUpdate(context.Background(), far, changed)
			if (err != nil) != tc.wantErr {
				t.Errorf("error=%v wantErr=%t", err, tc.wantErr)
			}
		})
	}
})

func TestSafetyContract(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Safety Contract Suite")
}
