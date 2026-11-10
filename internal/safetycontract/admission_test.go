package safetycontract

import (
	"context"
	"testing"

	commonannotations "github.com/medik8s/common/pkg/annotations"

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
		t.Run(tc.name, func(t *testing.T) {
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
}

func TestSafetyAdmissionLetsFinalizersGo(t *testing.T) {
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
		t.Run(tc.name, func(t *testing.T) {
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
}
