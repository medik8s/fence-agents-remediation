package safetycontract

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/go-logr/logr"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	farapi "github.com/medik8s/fence-agents-remediation/v5/api/v1alpha1"
	"github.com/medik8s/fence-agents-remediation/v5/internal/controller"
	"github.com/medik8s/fence-agents-remediation/v5/pkg/cli"
)

func TestSafetyTemplateNeverExecutes(t *testing.T) {
	far, config, secret := safetyFixture(t)
	tmpl := &farapi.FenceAgentsRemediationTemplate{ObjectMeta: far.ObjectMeta}
	tmpl.Spec.Template.Spec = far.Spec
	tmpl.Spec.Template.Spec.NodeBinding = nil
	tmpl.Spec.Template.Spec.NodeParameters = map[farapi.ParameterName]map[farapi.NodeName]string{"--ip": {"worker-a": "bmc.example"}}
	sample := intstr.FromInt32(1)
	tmpl.Spec.StatusValidationSample = &sample
	scheme := runtime.NewScheme()
	if err := farapi.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(tmpl).WithObjects(tmpl, secret).Build()
	var calls atomic.Int32
	recorder := record.NewFakeRecorder(10)
	executor := cli.NewFakeExecuter(c, func(context.Context, []string) (string, string, error) { calls.Add(1); return "ON", "", nil }, recorder)
	r := &controller.FenceAgentsRemediationTemplateReconciler{Client: c, Log: logr.Discard(), Executor: executor, Recorder: recorder, Safety: config}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(tmpl)}); err == nil {
		t.Error("unsafe template sampling accepted")
	}
	if calls.Load() != 0 {
		t.Fatal("safety template executed generic agent")
	}
}
