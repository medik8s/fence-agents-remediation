package safetycontract

import (
	"context"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	farapi "github.com/medik8s/fence-agents-remediation/v5/api/v1alpha1"
)

func safetyFixture(t *testing.T) (*farapi.FenceAgentsRemediation, farapi.SafetyConfig, *corev1.Secret) {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(server.Close)
	name := "credentials"
	return &farapi.FenceAgentsRemediation{ObjectMeta: metav1.ObjectMeta{Name: "worker-a", Namespace: "bmc", UID: "far-a", Generation: 1, Annotations: map[string]string{"remediation.medik8s.io/node-name": "worker-a"}}, Spec: farapi.FenceAgentsRemediationSpec{
		SafetyPolicy: farapi.RedfishOffV1, NodeBinding: &farapi.NodeBinding{Version: "v1", Name: "worker-a", UID: "node-a", SystemUUID: systemUUID},
		Agent: "fence_redfish", RetryCount: 1, RetryInterval: metav1.Duration{Duration: time.Second}, Timeout: metav1.Duration{Duration: time.Second}, RemediationStrategy: farapi.OutOfServiceTaintRemediationStrategy,
		SharedParameters: map[farapi.ParameterName]string{"--action": "off", "--ip": "bmc.example", "--systems-uri": "/redfish/v1/Systems/1"}, SharedSecretName: &name,
	}}, farapi.SafetyConfig{Enabled: true, CredentialNamespace: "bmc", TrustedNHCCreator: "system:serviceaccount:operators:nhc", CA: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})}, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "bmc", UID: "secret-a", ResourceVersion: "1"}, Data: map[string][]byte{"username": []byte("user"), "password": []byte("password")}}
}

func TestSafetyValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(*farapi.FenceAgentsRemediation)
		wantErr bool
	}{
		{name: "valid", mutate: func(*farapi.FenceAgentsRemediation) {}},
		{name: "legacy missing binding", mutate: func(f *farapi.FenceAgentsRemediation) { f.Spec.NodeBinding = nil }, wantErr: true},
		{name: "unknown policy", mutate: func(f *farapi.FenceAgentsRemediation) { f.Spec.SafetyPolicy = "unknown" }, wantErr: true},
		{name: "wrong version", mutate: func(f *farapi.FenceAgentsRemediation) { f.Spec.NodeBinding.Version = "v2" }, wantErr: true},
		{name: "annotation differs", mutate: func(f *farapi.FenceAgentsRemediation) { f.Annotations["remediation.medik8s.io/node-name"] = "worker-b" }, wantErr: true},
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
		t.Run(tc.name, func(t *testing.T) {
			f, _, _ := safetyFixture(t)
			tc.mutate(f)
			if err := farapi.ValidateSafetyFAR(f); (err != nil) != tc.wantErr {
				t.Errorf("error=%v wantErr=%t", err, tc.wantErr)
			}
		})
	}
}

func TestSafetySecretsAndTargets(t *testing.T) {
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
		t.Run(tc.name, func(t *testing.T) {
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
}
