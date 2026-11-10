package safetycontract

import (
	"context"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	farapi "github.com/medik8s/fence-agents-remediation/v5/api/v1alpha1"
)

func TestSafetyAdmission(t *testing.T) {
	for _, tc := range []struct {
		name                                                                string
		untrusted, missingConfig, replacement, mutateSpec, templateIdentity bool
		wantErr                                                             bool
		duplicate                                                           bool
	}{
		{name: "trusted NHC create"},
		{name: "duplicate known UUID", duplicate: true, wantErr: true},
		{name: "manual CR denied", untrusted: true, wantErr: true},
		{name: "missing creator config", missingConfig: true, wantErr: true},
		{name: "replacement UID", replacement: true, wantErr: true},
		{name: "immutable effective spec", mutateSpec: true, wantErr: true},
		{name: "template must omit identity", templateIdentity: true, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			far, config, secret := safetyFixture(t)
			node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: far.Spec.NodeBinding.Name, UID: "node-a"}, Status: corev1.NodeStatus{NodeInfo: corev1.NodeSystemInfo{SystemUUID: systemUUID}}}
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
			case tc.templateIdentity:
				tmpl := &farapi.FenceAgentsRemediationTemplate{ObjectMeta: far.ObjectMeta}
				tmpl.Spec.Template.Spec = far.Spec
				_, err = (&farapi.FARTemplateValidator{Client: c, APIReader: c, Safety: config}).ValidateCreate(ctx, tmpl)
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
}
