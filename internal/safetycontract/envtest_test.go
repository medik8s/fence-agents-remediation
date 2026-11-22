package safetycontract

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-logr/logr/funcr"
	. "github.com/onsi/ginkgo/v2"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/yaml"

	farapi "github.com/medik8s/fence-agents-remediation/v5/api/v1alpha1"
	"github.com/medik8s/fence-agents-remediation/v5/internal/controller"
	"github.com/medik8s/fence-agents-remediation/v5/pkg/utils"
)

var _ = It("Safety-mode API schema and RBAC", func() {
	t := GinkgoTB()
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Fatal("explicit local KUBEBUILDER_ASSETS required")
	}
	if value := os.Getenv("USE_EXISTING_CLUSTER"); value != "" && value != "false" {
		t.Fatal("USE_EXISTING_CLUSTER must be unset or false")
	}
	useExisting := false
	environment := &envtest.Environment{UseExistingCluster: &useExisting, CRDDirectoryPaths: []string{filepath.Join("..", "..", "config", "crd", "bases")}, ErrorIfCRDPathMissing: true}
	environment.ControlPlane.GetAPIServer().Configure().Set("authorization-mode", "RBAC")
	config, err := environment.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := environment.Stop(); err != nil {
			t.Error(err)
		}
	})
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{corev1.AddToScheme, rbacv1.AddToScheme, farapi.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	admin, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, name := range []string{"bmc", "foreign"} {
		if err := admin.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}); err != nil {
			t.Fatal(err)
		}
	}
	By("schema without webhooks", func() {
		t := GinkgoTB()
		far, _, _ := safetyFixture(t)
		far.UID = ""
		far.Generation = 0
		far.Name = "schema-check"
		if err := admin.Create(ctx, far); err != nil {
			t.Fatal(err)
		}
		changed := far.DeepCopy()
		changed.Spec.SharedParameters["--ip"] = "other.example"
		if err := admin.Update(ctx, changed); !apierrors.IsInvalid(err) {
			t.Fatalf("target mutation error=%v", err)
		}
		changed = far.DeepCopy()
		changed.Spec.SafetyPolicy = ""
		if err := admin.Update(ctx, changed); !apierrors.IsInvalid(err) {
			t.Fatalf("policy removal error=%v", err)
		}
		zero := &unstructured.Unstructured{Object: map[string]any{"apiVersion": farapi.GroupVersion.String(), "kind": "FenceAgentsRemediation", "metadata": map[string]any{"name": "zero-retry", "namespace": "bmc"}}}
		data, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&far.Spec)
		if err != nil {
			t.Fatal(err)
		}
		data["retrycount"] = int64(0)
		zero.Object["spec"] = data
		if err := admin.Create(ctx, zero); !apierrors.IsInvalid(err) {
			t.Fatalf("retry zero error=%v", err)
		}
		legacy := far.DeepCopy()
		legacy.Name = "legacy"
		legacy.ResourceVersion = ""
		legacy.UID = ""
		legacy.Spec.SafetyPolicy = ""
		if err := admin.Create(ctx, legacy); err != nil {
			t.Fatalf("legacy schema changed: %v", err)
		}
	})
	By("known Node UUID uniqueness", func() {
		t := GinkgoTB()
		node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "unique-target"}}
		if err := admin.Create(ctx, node); err != nil {
			t.Fatal(err)
		}
		node.Status.NodeInfo.SystemUUID = systemUUID
		if err := admin.Status().Update(ctx, node); err != nil {
			t.Fatal(err)
		}
		binding := farapi.NodeBinding{Name: node.Name, UID: string(node.UID), SystemUUID: systemUUID}
		selected, err := farapi.ReadUniqueBoundNode(ctx, admin, binding)
		if err != nil || selected.UID != node.UID || selected.ResourceVersion == "" {
			t.Fatalf("unique target error=%v", err)
		}
		other := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "uuid-duplicate"}}
		if err := admin.Create(ctx, other); err != nil {
			t.Fatal(err)
		}
		other.Status.NodeInfo.SystemUUID = "00112233-4455-4677-8899-AABBCCDDEEFF"
		if err := admin.Status().Update(ctx, other); err != nil {
			t.Fatal(err)
		}
		if _, err := farapi.ReadUniqueBoundNode(ctx, admin, binding); err == nil {
			t.Fatal("real API duplicate UUID accepted")
		}
	})
	By("UID and resourceVersion conditional taints", func() {
		t := GinkgoTB()
		for _, removing := range []bool{false, true} {
			name := "conditional-add"
			if removing {
				name = "conditional-cleanup"
			}
			old := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}}
			if removing {
				old.Spec.Taints = []corev1.Taint{utils.CreateOutOfServiceTaint()}
			}
			if err := admin.Create(ctx, old); err != nil {
				t.Fatal(err)
			}
			old.Status.NodeInfo.SystemUUID = systemUUID
			if err := admin.Status().Update(ctx, old); err != nil {
				t.Fatal(err)
			}
			binding := farapi.NodeBinding{Name: old.Name, UID: string(old.UID), SystemUUID: systemUUID}
			if err := admin.Delete(ctx, old); err != nil {
				t.Fatal(err)
			}
			replacement := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}}
			if removing {
				replacement.Spec.Taints = []corev1.Taint{utils.CreateOutOfServiceTaint()}
			}
			if err := admin.Create(ctx, replacement); err != nil {
				t.Fatal(err)
			}
			replacement.Status.NodeInfo.SystemUUID = systemUUID
			if err := admin.Status().Update(ctx, replacement); err != nil {
				t.Fatal(err)
			}
			var add, remove []corev1.Taint
			if removing {
				remove = []corev1.Taint{utils.CreateOutOfServiceTaint()}
			} else {
				add = []corev1.Taint{utils.CreateOutOfServiceTaint()}
			}
			err := utils.UpdateBoundTaints(ctx, admin, old, binding, add, remove)
			if !apierrors.IsConflict(err) && !apierrors.IsInvalid(err) {
				t.Fatalf("stale Node write error=%v", err)
			}
			if err := admin.Get(ctx, client.ObjectKey{Name: name}, replacement); err != nil {
				t.Fatal(err)
			}
			if string(replacement.UID) == binding.UID || hasOOS(replacement) != removing {
				t.Fatal("stale helper mutated a replacement Node")
			}
			binding.UID = string(replacement.UID)
			if err := utils.UpdateBoundTaints(ctx, admin, replacement, binding, add, remove); err != nil {
				t.Fatal(err)
			}
			if err := admin.Get(ctx, client.ObjectKey{Name: name}, replacement); err != nil {
				t.Fatal(err)
			}
			if hasOOS(replacement) == removing {
				t.Fatal("fresh matching Node positive control failed")
			}
		}
	})
	By("namespaced read grants", func() {
		t := GinkgoTB()
		var role rbacv1.Role
		data, err := os.ReadFile(filepath.Join("..", "..", "config", "rbac", "safety", "credential_role.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		if err := yaml.UnmarshalStrict(data, &role); err != nil {
			t.Fatal(err)
		}
		role.Namespace = "bmc"
		if err := admin.Create(ctx, &role); err != nil {
			t.Fatal(err)
		}
		var clusterRole rbacv1.ClusterRole
		data, err = os.ReadFile(filepath.Join("..", "..", "config", "rbac", "safety", "cluster_role.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		if err := yaml.UnmarshalStrict(data, &clusterRole); err != nil {
			t.Fatal(err)
		}
		if err := admin.Create(ctx, &clusterRole); err != nil {
			t.Fatal(err)
		}
		username := "system:serviceaccount:operators:far"
		subject := rbacv1.Subject{Kind: "User", APIGroup: rbacv1.GroupName, Name: username}
		if err := admin.Create(ctx, &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "safety", Namespace: "bmc"}, RoleRef: rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: role.Name}, Subjects: []rbacv1.Subject{subject}}); err != nil {
			t.Fatal(err)
		}
		if err := admin.Create(ctx, &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "safety"}, RoleRef: rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: clusterRole.Name}, Subjects: []rbacv1.Subject{subject}}); err != nil {
			t.Fatal(err)
		}
		for _, ns := range []string{"bmc", "foreign"} {
			if err := admin.Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "credentials", Namespace: ns}}); err != nil {
				t.Fatal(err)
			}
		}
		impersonated := rest.CopyConfig(config)
		impersonated.Impersonate.UserName = username
		restricted, err := client.New(impersonated, client.Options{Scheme: scheme})
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			err = restricted.Get(ctx, client.ObjectKey{Namespace: "bmc", Name: "credentials"}, &corev1.Secret{})
			if err == nil {
				err = restricted.List(ctx, &corev1.NodeList{})
			}
			if err == nil {
				break
			}
			if !apierrors.IsForbidden(err) || time.Now().After(deadline) {
				t.Fatal(err)
			}
			time.Sleep(20 * time.Millisecond)
		}
		if err := restricted.Get(ctx, client.ObjectKey{Namespace: "foreign", Name: "credentials"}, &corev1.Secret{}); !apierrors.IsForbidden(err) {
			t.Fatalf("foreign Secret error=%v", err)
		}
		if err := restricted.List(ctx, &corev1.SecretList{}); !apierrors.IsForbidden(err) {
			t.Fatalf("cluster-wide Secret list error=%v", err)
		}
		if err := restricted.Delete(ctx, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "not-present"}}); !apierrors.IsForbidden(err) {
			t.Fatalf("Node deletion error=%v", err)
		}
		for _, namespace := range []string{"bmc", "foreign"} {
			far, _, _ := safetyFixture(t)
			far.ObjectMeta = metav1.ObjectMeta{Name: "scope-far", Namespace: namespace}
			tmpl := &farapi.FenceAgentsRemediationTemplate{ObjectMeta: metav1.ObjectMeta{Name: "scope-template", Namespace: namespace}}
			tmpl.Spec.Template.Spec = *far.Spec.DeepCopy()
			for _, object := range []client.Object{far, tmpl} {
				if err := admin.Create(ctx, object); err != nil {
					t.Fatal(err)
				}
				allowed := namespace == "bmc"
				check := func(operation string, err error) {
					if allowed && err != nil {
						t.Fatalf("local %T %s error=%v", object, operation, err)
					}
					if !allowed && !apierrors.IsForbidden(err) {
						t.Errorf("foreign %T %s error=%v, want Forbidden", object, operation, err)
					}
				}
				copy := object.DeepCopyObject().(client.Object)
				check("get", restricted.Get(ctx, client.ObjectKeyFromObject(object), copy))
				statusCopy := object.DeepCopyObject().(client.Object)
				check("get status", restricted.SubResource("status").Get(ctx, object, statusCopy))
				copy.SetLabels(map[string]string{"scope-check": "updated"})
				check("update", restricted.Update(ctx, copy))
				condition := metav1.Condition{Type: "ScopeControl", Status: metav1.ConditionTrue, Reason: "ScopeControl", Message: "Namespace permission control", LastTransitionTime: metav1.Now()}
				switch typed := copy.(type) {
				case *farapi.FenceAgentsRemediation:
					typed.Status.Conditions = []metav1.Condition{condition}
				case *farapi.FenceAgentsRemediationTemplate:
					typed.Status.Conditions = []metav1.Condition{condition}
				}
				check("update status", restricted.Status().Update(ctx, copy))
			}
		}
	})
})

var _ = DescribeTable("cleans up after the API server increases generation on delete", func(retained, rejectWrite bool) {
	t := GinkgoTB()
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Fatal("explicit local KUBEBUILDER_ASSETS required")
	}
	useExisting := false
	environment := &envtest.Environment{UseExistingCluster: &useExisting, CRDDirectoryPaths: []string{filepath.Join("..", "..", "config", "crd", "bases")}, ErrorIfCRDPathMissing: true}
	config, err := environment.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := environment.Stop(); err != nil {
			t.Error(err)
		}
	})
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{corev1.AddToScheme, farapi.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	admin, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := admin.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "bmc"}}); err != nil {
		t.Fatal(err)
	}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "cleanup-after-delete"}, Spec: corev1.NodeSpec{Taints: []corev1.Taint{utils.CreateOutOfServiceTaint(), utils.CreateRemediationTaint()}}}
	if err := admin.Create(ctx, node); err != nil {
		t.Fatal(err)
	}
	node.Status.NodeInfo.SystemUUID = systemUUID
	if err := admin.Status().Update(ctx, node); err != nil {
		t.Fatal(err)
	}
	far, _, _ := safetyFixture(t)
	far.ObjectMeta = metav1.ObjectMeta{Name: "cleanup-after-delete", Namespace: "bmc", Finalizers: []string{farapi.FARFinalizer}, Annotations: identityAnnotations(node.Name, string(node.UID), systemUUID)}
	const otherFinalizer = "example.com/other-controller"
	if retained {
		far.Finalizers = append(far.Finalizers, otherFinalizer)
	}
	if err := admin.Create(ctx, far); err != nil {
		t.Fatal(err)
	}
	snapshot, err := farapi.NewSafetySnapshot(far, farapi.SafetyTarget{Endpoint: "https://bmc.example", SystemURI: "/redfish/v1/Systems/1"})
	if err != nil {
		t.Fatal(err)
	}
	far.Status.SafetySnapshot = &snapshot
	if err := admin.Status().Update(ctx, far); err != nil {
		t.Fatal(err)
	}
	if err := admin.Delete(ctx, far); err != nil {
		t.Fatal(err)
	}
	deleting := &farapi.FenceAgentsRemediation{}
	if err := admin.Get(ctx, client.ObjectKeyFromObject(far), deleting); err != nil {
		t.Fatal(err)
	}
	if deleting.Generation == snapshot.Generation {
		t.Fatalf("the API server kept generation %d on delete; this test needs the bump", deleting.Generation)
	}
	var records []string
	logger := funcr.NewJSON(func(line string) { records = append(records, line) }, funcr.Options{})
	writer := &cleanupWriteClient{Client: admin, rejectFinalizerWrite: rejectWrite}
	reconciler := &controller.FenceAgentsRemediationReconciler{Client: writer, APIReader: admin, Log: logger, Recorder: record.NewFakeRecorder(10)}
	cleanupMessages := func() int {
		return strings.Count(strings.Join(records, "\n"), `"phase":"CleanedUp"`)
	}
	request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(far)}
	if rejectWrite {
		if _, err := reconciler.Reconcile(ctx, request); err == nil {
			t.Fatal("cleanup accepted a rejected finalizer write")
		}
		if cleanupMessages() != 0 {
			t.Fatal("cleanup was logged before its finalizer write succeeded")
		}
		if err := admin.Get(ctx, client.ObjectKeyFromObject(far), deleting); err != nil {
			t.Fatal(err)
		}
		if !controllerutil.ContainsFinalizer(deleting, farapi.FARFinalizer) {
			t.Fatal("the rejected update removed the FAR finalizer")
		}
	}
	for range 3 {
		if _, err := reconciler.Reconcile(ctx, request); err != nil {
			t.Fatalf("cleanup after delete: %v", err)
		}
	}
	if count := cleanupMessages(); count != 1 {
		t.Errorf("CleanedUp logged %d times for one completed cleanup", count)
	}
	if err := admin.Get(ctx, client.ObjectKeyFromObject(node), node); err != nil {
		t.Fatal(err)
	}
	oos, remediation := utils.CreateOutOfServiceTaint(), utils.CreateRemediationTaint()
	if utils.TaintExists(node.Spec.Taints, &oos) || utils.TaintExists(node.Spec.Taints, &remediation) {
		t.Errorf("remediation taints left on the Node: %v", node.Spec.Taints)
	}
	if retained {
		node.Spec.Taints = append(node.Spec.Taints, oos, remediation)
		if err := admin.Update(ctx, node); err != nil {
			t.Fatal(err)
		}
		if _, err := reconciler.Reconcile(ctx, request); err != nil {
			t.Fatal(err)
		}
		if err := admin.Get(ctx, client.ObjectKeyFromObject(node), node); err != nil {
			t.Fatal(err)
		}
		if utils.TaintExists(node.Spec.Taints, &oos) || utils.TaintExists(node.Spec.Taints, &remediation) {
			t.Fatal("suppressing the duplicate log bypassed the existing taint cleanup")
		}
		if cleanupMessages() != 1 {
			t.Fatal("cleanup without our finalizer produced another lifecycle event")
		}
		if err := admin.Get(ctx, client.ObjectKeyFromObject(far), deleting); err != nil {
			t.Fatal(err)
		}
		if len(deleting.Finalizers) != 1 || deleting.Finalizers[0] != otherFinalizer {
			t.Fatalf("other controller's finalizer was not retained: %v", deleting.Finalizers)
		}
		deleting.Finalizers = nil
		if err := admin.Update(ctx, deleting); err != nil {
			t.Fatal(err)
		}
	} else if err := admin.Get(ctx, client.ObjectKeyFromObject(far), deleting); !apierrors.IsNotFound(err) {
		t.Errorf("remediation still present: %v", err)
	}
},
	Entry("without another finalizer", false, false),
	Entry("while another controller retains the object", true, false),
	Entry("after a finalizer write is rejected", true, true),
)

func hasOOS(node *corev1.Node) bool {
	taint := utils.CreateOutOfServiceTaint()
	return utils.TaintExists(node.Spec.Taints, &taint)
}

type cleanupWriteClient struct {
	client.Client
	rejectFinalizerWrite bool
}

func (c *cleanupWriteClient) Update(ctx context.Context, obj client.Object, options ...client.UpdateOption) error {
	if _, ok := obj.(*farapi.FenceAgentsRemediation); ok && c.rejectFinalizerWrite {
		c.rejectFinalizerWrite = false
		return errors.New("cleanup finalizer write rejected")
	}
	return c.Client.Update(ctx, obj, options...)
}
