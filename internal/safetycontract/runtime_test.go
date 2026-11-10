package safetycontract

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr"
	commonannotations "github.com/medik8s/common/pkg/annotations"
	commonconditions "github.com/medik8s/common/pkg/conditions"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	farapi "github.com/medik8s/fence-agents-remediation/v5/api/v1alpha1"
	"github.com/medik8s/fence-agents-remediation/v5/internal/controller"
	"github.com/medik8s/fence-agents-remediation/v5/pkg/cli"
	"github.com/medik8s/fence-agents-remediation/v5/pkg/utils"
)

const systemUUID = "00112233-4455-4677-8899-aabbccddeeff"

type harness struct {
	client     client.Client
	reconciler *controller.FenceAgentsRemediationReconciler
	far        *farapi.FenceAgentsRemediation
	calls      atomic.Int32
}

func newHarness(t *testing.T, retries int, runnerErr error) *harness {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{corev1.AddToScheme, farapi.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker-a", UID: "node-a"}, Status: corev1.NodeStatus{NodeInfo: corev1.NodeSystemInfo{SystemUUID: systemUUID}}}
	far := &farapi.FenceAgentsRemediation{ObjectMeta: metav1.ObjectMeta{Name: node.Name, Namespace: "bmc", UID: "far-a", Finalizers: []string{farapi.FARFinalizer}}, Spec: farapi.FenceAgentsRemediationSpec{Agent: "fence_redfish", RetryCount: retries, RetryInterval: metav1.Duration{Duration: time.Millisecond}, Timeout: metav1.Duration{Duration: time.Second}, RemediationStrategy: farapi.OutOfServiceTaintRemediationStrategy, SharedParameters: map[farapi.ParameterName]string{"--action": "off"}, NodeParameters: map[farapi.ParameterName]map[farapi.NodeName]string{"--ip": {farapi.NodeName(node.Name): "192.0.2.1"}}}}
	utils.UpdateConditions(utils.RemediationStarted, far, logr.Discard())
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(far).WithObjects(node, far).Build()
	h := &harness{client: c, far: far}
	recorder := record.NewFakeRecorder(100)
	executor := cli.NewFakeExecuter(c, func(context.Context, []string) (string, string, error) { h.calls.Add(1); return "", "", runnerErr }, recorder)
	h.reconciler = &controller.FenceAgentsRemediationReconciler{Client: c, Scheme: scheme, Log: logr.Discard(), Recorder: recorder, Executor: executor}
	return h
}

func (h *harness) reconcile(t *testing.T) {
	t.Helper()
	if _, err := h.reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(h.far)}); err != nil {
		t.Fatal(err)
	}
}

func (h *harness) getFAR(t *testing.T) *farapi.FenceAgentsRemediation {
	t.Helper()
	far := &farapi.FenceAgentsRemediation{}
	if err := h.client.Get(context.Background(), client.ObjectKeyFromObject(h.far), far); err != nil {
		t.Fatal(err)
	}
	return far
}

func (h *harness) node(t *testing.T) *corev1.Node {
	t.Helper()
	node := &corev1.Node{}
	if err := h.client.Get(context.Background(), client.ObjectKey{Name: h.far.Name}, node); err != nil {
		t.Fatal(err)
	}
	return node
}

func (h *harness) execute(t *testing.T) {
	t.Helper()
	h.reconcile(t)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		condition := meta.FindStatusCondition(h.getFAR(t).Status.Conditions, utils.FenceAgentActionSucceededType)
		if condition != nil && condition.Status != metav1.ConditionUnknown {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("executor did not finish")
}

func (h *harness) seedSuccess(t *testing.T) {
	t.Helper()
	far := h.getFAR(t)
	utils.UpdateConditions(utils.FenceAgentSucceeded, far, logr.Discard())
	if err := h.client.Status().Update(context.Background(), far); err != nil {
		t.Fatal(err)
	}
}

func (h *harness) recreateNode(t *testing.T) {
	t.Helper()
	node := h.node(t)
	if err := h.client.Delete(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	node.ResourceVersion = ""
	node.UID = "node-b"
	if err := h.client.Create(context.Background(), node); err != nil {
		t.Fatal(err)
	}
}

func hasOOS(node *corev1.Node) bool {
	taint := utils.CreateOutOfServiceTaint()
	return utils.TaintExists(node.Spec.Taints, &taint)
}

func TestExecutorControls(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "success"
		var runnerErr error
		if fail {
			name = "failure"
			runnerErr = errors.New("runner failed")
		}
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, 1, runnerErr)
			h.execute(t)
			h.reconcile(t)
			if h.calls.Load() != 1 || hasOOS(h.node(t)) == fail {
				t.Fatal("executor control failed")
			}
		})
	}
}

func enableSafety(t *testing.T, h *harness) {
	t.Helper()
	fixture, config, _ := safetyFixture(t)
	far := h.getFAR(t)
	far.Spec.SafetyPolicy = fixture.Spec.SafetyPolicy
	if far.Annotations == nil {
		far.Annotations = map[string]string{}
	}
	for key, value := range fixture.Annotations {
		far.Annotations[key] = value
	}
	far.Spec.RetryInterval.Duration = time.Second
	h.reconciler.Safety = config
	h.reconciler.APIReader = h.client
	if err := h.client.Update(context.Background(), far); err != nil {
		t.Fatal(err)
	}
}

func TestRetryZeroCannotRelease(t *testing.T) {
	h := newHarness(t, 0, nil)
	enableSafety(t, h)
	if _, err := h.reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(h.far)}); err == nil {
		t.Error("invalid retry count accepted")
	}
	if h.calls.Load() != 0 {
		t.Fatal("retry-zero control unexpectedly executed")
	}
	if hasOOS(h.node(t)) {
		t.Error("zero executions released node")
	}
}

func TestPersistedSuccessCannotReleaseReplacement(t *testing.T) {
	h := newHarness(t, 1, nil)
	enableSafety(t, h)
	h.seedSuccess(t)
	h.recreateNode(t)
	_, _ = h.reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(h.far)})
	if h.calls.Load() != 0 {
		t.Fatal("persisted-success control executed")
	}
	if hasOOS(h.node(t)) {
		t.Error("persisted success released replacement UID")
	}
}

func TestCleanupRemainsUIDScopedWithDuplicateUUID(t *testing.T) {
	h := newHarness(t, 1, nil)
	enableSafety(t, h)
	node := h.node(t)
	node.Spec.Taints = []corev1.Taint{utils.CreateOutOfServiceTaint()}
	if err := h.client.Update(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	other := node.DeepCopy()
	other.Name = "worker-b"
	other.UID = "node-b"
	other.ResourceVersion = ""
	if err := h.client.Create(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	if err := h.client.Delete(context.Background(), h.getFAR(t)); err != nil {
		t.Fatal(err)
	}
	h.reconcile(t)
	if hasOOS(h.node(t)) {
		t.Fatal("duplicate UUID blocked bound-UID cleanup")
	}
	if err := h.client.Get(context.Background(), client.ObjectKey{Name: other.Name}, other); err != nil {
		t.Fatal(err)
	}
	if !hasOOS(other) {
		t.Fatal("cleanup touched duplicate-UUID Node with a different UID")
	}
}

func TestCleanupRejectsForeignNamespace(t *testing.T) {
	h := newHarness(t, 1, nil)
	enableSafety(t, h)
	far := h.getFAR(t)
	far.Namespace = "foreign"
	far.ResourceVersion = ""
	far.UID = "foreign-far"
	if err := h.client.Create(context.Background(), far); err != nil {
		t.Fatal(err)
	}
	node := h.node(t)
	node.Spec.Taints = []corev1.Taint{utils.CreateOutOfServiceTaint()}
	if err := h.client.Update(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	if err := h.client.Delete(context.Background(), far); err != nil {
		t.Fatal(err)
	}
	if _, err := h.reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(far)}); err == nil {
		t.Error("foreign namespace cleanup accepted")
	}
	if !hasOOS(h.node(t)) {
		t.Fatal("foreign namespace removed selected Node taint")
	}
}

func TestCleanupRejectsMutatedSnapshot(t *testing.T) {
	h := newHarness(t, 1, nil)
	enableSafety(t, h)
	far := h.getFAR(t)
	snapshot, err := farapi.NewSafetySnapshot(far, farapi.SafetyTarget{})
	if err != nil {
		t.Fatal(err)
	}
	far.Status.SafetySnapshot = &snapshot
	if err := h.client.Status().Update(context.Background(), far); err != nil {
		t.Fatal(err)
	}
	far = h.getFAR(t)
	far.Spec.NodeParameters["--ip"]["worker-a"] = "other.example"
	if err := h.client.Update(context.Background(), far); err != nil {
		t.Fatal(err)
	}
	node := h.node(t)
	node.Spec.Taints = []corev1.Taint{utils.CreateOutOfServiceTaint()}
	if err := h.client.Update(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	if err := h.client.Delete(context.Background(), h.getFAR(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := h.reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(h.far)}); err == nil {
		t.Error("cleanup accepted changed effective spec")
	}
	if !hasOOS(h.node(t)) {
		t.Fatal("cleanup changed taints after spec mutation")
	}
}

func TestCleanupCannotRemoveReplacementTaint(t *testing.T) {
	h := newHarness(t, 1, nil)
	enableSafety(t, h)
	h.seedSuccess(t)
	h.recreateNode(t)
	node := h.node(t)
	node.Spec.Taints = []corev1.Taint{utils.CreateOutOfServiceTaint()}
	if err := h.client.Update(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	if err := h.client.Delete(context.Background(), h.getFAR(t)); err != nil {
		t.Fatal(err)
	}
	h.reconcile(t)
	if !hasOOS(h.node(t)) {
		t.Error("cleanup removed replacement taint")
	}
}

func TestTerminatingSafetyRemediationIsCleanedUpWithSafetyModeOff(t *testing.T) {
	h := newHarness(t, 1, nil)
	enableSafety(t, h)
	node := h.node(t)
	node.Spec.Taints = []corev1.Taint{utils.CreateOutOfServiceTaint()}
	if err := h.client.Update(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	if err := h.client.Delete(context.Background(), h.getFAR(t)); err != nil {
		t.Fatal(err)
	}
	h.reconciler.Safety = farapi.SafetyConfig{}
	h.reconcile(t)
	if hasOOS(h.node(t)) {
		t.Error("out-of-service taint left on the Node")
	}
	if err := h.client.Get(context.Background(), client.ObjectKeyFromObject(h.far), &farapi.FenceAgentsRemediation{}); !apierrors.IsNotFound(err) {
		t.Errorf("remediation still present: %v", err)
	}
}

func TestTerminatingSafetyRemediationWithChangedSnapshotKeepsItsTaints(t *testing.T) {
	h := newHarness(t, 1, nil)
	enableSafety(t, h)
	node := h.node(t)
	node.Spec.Taints = []corev1.Taint{utils.CreateOutOfServiceTaint()}
	if err := h.client.Update(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	far := h.getFAR(t)
	far.Status.SafetySnapshot = &farapi.SafetySnapshot{FARUID: string(far.UID), SpecDigest: "persisted-for-another-spec"}
	if err := h.client.Status().Update(context.Background(), far); err != nil {
		t.Fatal(err)
	}
	if err := h.client.Delete(context.Background(), h.getFAR(t)); err != nil {
		t.Fatal(err)
	}
	h.reconciler.Safety = farapi.SafetyConfig{}
	if _, err := h.reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(h.far)}); err == nil {
		t.Error("cleanup accepted a snapshot that differs from the remediation")
	}
	if !hasOOS(h.node(t)) {
		t.Error("out-of-service taint removed although the snapshot differs")
	}
	if !controllerutil.ContainsFinalizer(h.getFAR(t), farapi.FARFinalizer) {
		t.Error("finalizer removed although the snapshot differs")
	}
}

type failingRemediationList struct{ client.Reader }

func (r failingRemediationList) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if _, ok := list.(*farapi.FenceAgentsRemediationList); ok {
		return fmt.Errorf("remediation list unavailable")
	}
	return r.Reader.List(ctx, list, opts...)
}

func TestCleanupLeavesTaintsAnotherRemediationNeeds(t *testing.T) {
	for _, tc := range []struct {
		name                         string
		otherReleased, otherDeleting bool
		otherUnbound, otherLegacy    bool
		otherNodeUID                 string
		listFails                    bool
		wantTaints, wantFinalizer    bool
	}{
		{name: "other remediation still running", wantTaints: true},
		{name: "other remediation released", otherReleased: true, wantTaints: true},
		{name: "other remediation deleted with it", otherDeleting: true},
		{name: "remediation list unavailable", listFails: true, wantTaints: true, wantFinalizer: true},
		{name: "other remediation never got its finalizer", otherUnbound: true},
		{name: "other remediation is not a safety remediation", otherLegacy: true},
		{name: "other remediation bound to another Node", otherNodeUID: "node-b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, 1, nil)
			enableSafety(t, h)
			node := h.node(t)
			node.Spec.Taints = []corev1.Taint{utils.CreateOutOfServiceTaint(), utils.CreateRemediationTaint()}
			if err := h.client.Update(context.Background(), node); err != nil {
				t.Fatal(err)
			}
			other := h.getFAR(t).DeepCopy()
			other.ObjectMeta = metav1.ObjectMeta{Name: "worker-a-escalated", Namespace: other.Namespace, Annotations: other.Annotations, Finalizers: []string{farapi.FARFinalizer}}
			other.Status = farapi.FenceAgentsRemediationStatus{}
			if tc.otherUnbound {
				other.Finalizers = nil
			}
			if tc.otherLegacy {
				other.Spec.SafetyPolicy = ""
			}
			if tc.otherNodeUID != "" {
				other.Annotations = map[string]string{}
				for key, value := range h.getFAR(t).Annotations {
					other.Annotations[key] = value
				}
				other.Annotations[commonannotations.NodeUIDAnnotation] = tc.otherNodeUID
			}
			if err := h.client.Create(context.Background(), other); err != nil {
				t.Fatal(err)
			}
			if tc.otherReleased {
				other.Status.OffProof = &farapi.OffProof{ConfirmedAt: metav1.Now()}
				meta.SetStatusCondition(&other.Status.Conditions, metav1.Condition{Type: commonconditions.SucceededType, Status: metav1.ConditionTrue, Reason: "IdentityBoundRelease"})
				if err := h.client.Status().Update(context.Background(), other); err != nil {
					t.Fatal(err)
				}
			}
			if tc.otherDeleting {
				if err := h.client.Delete(context.Background(), other); err != nil {
					t.Fatal(err)
				}
			}
			if err := h.client.Delete(context.Background(), h.getFAR(t)); err != nil {
				t.Fatal(err)
			}
			if tc.listFails {
				h.reconciler.APIReader = failingRemediationList{Reader: h.client}
			}
			_, err := h.reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(h.far)})
			if tc.listFails && err == nil {
				t.Error("cleanup went ahead without knowing the other remediations")
			}
			taints := h.node(t).Spec.Taints
			if hasTaints := len(taints) == 2; hasTaints != tc.wantTaints {
				t.Errorf("taints kept=%t, want %t (%v)", hasTaints, tc.wantTaints, taints)
			}
			current := &farapi.FenceAgentsRemediation{}
			getErr := h.client.Get(context.Background(), client.ObjectKeyFromObject(h.far), current)
			if hasFinalizer := getErr == nil && controllerutil.ContainsFinalizer(current, farapi.FARFinalizer); hasFinalizer != tc.wantFinalizer {
				t.Errorf("finalizer kept=%t, want %t (get error %v)", hasFinalizer, tc.wantFinalizer, getErr)
			}
		})
	}
}
