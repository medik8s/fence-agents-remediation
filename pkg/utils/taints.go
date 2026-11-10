package utils

// Inspired from SNR - https://github.com/medik8s/self-node-remediation/blob/main/pkg/utils/taints.go
import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/medik8s/fence-agents-remediation/v5/api/v1alpha1"
)

var (
	loggerTaint = ctrl.Log.WithName("taints")
)

// Taints are unique by key:effect
// Regardless of the taint's value

// TaintExists checks if the given taint exists in list of taints. Returns true if exists false otherwise.
func TaintExists(taints []corev1.Taint, taintToFind *corev1.Taint) bool {
	for _, taint := range taints {
		if taint.MatchTaint(taintToFind) {
			return true
		}
	}
	return false
}

// deleteTaint removes all the taints that have the same key and effect to given taintToDelete.
func deleteTaint(taints []corev1.Taint, taintToDelete *corev1.Taint) ([]corev1.Taint, bool) {
	var newTaints []corev1.Taint
	deleted := false
	for i := range taints {
		if taintToDelete.MatchTaint(&taints[i]) {
			deleted = true
			continue
		}
		newTaints = append(newTaints, taints[i])
	}
	return newTaints, deleted
}

// CreateRemediationTaint returns a remediation NoSchedule taint
func CreateRemediationTaint() corev1.Taint {
	return corev1.Taint{
		Key:    v1alpha1.FARNoScheduleTaintKey,
		Effect: corev1.TaintEffectNoSchedule,
	}
}

// CreateOutOfServiceTaint returns an OutOfService taint
func CreateOutOfServiceTaint() corev1.Taint {
	now := metav1.Now()
	return corev1.Taint{
		Key:       corev1.TaintNodeOutOfService,
		Value:     "nodeshutdown",
		Effect:    corev1.TaintEffectNoExecute,
		TimeAdded: &now,
	}
}

// AppendTaint appends new taint to the taint list when it is not present.
// It returns bool if a taint was appended, and an error if it fails in the process
func AppendTaint(r client.Client, nodeName string, taint corev1.Taint) (bool, error) {
	// find node by name
	node, err := GetNodeWithName(r, nodeName)
	if node == nil {
		return false, err
	}

	// check if taint doesn't exist
	if TaintExists(node.Spec.Taints, &taint) {
		return false, nil
	}
	// add the taint to the taint list
	now := metav1.Now()
	taint.TimeAdded = &now
	node.Spec.Taints = append(node.Spec.Taints, taint)

	// update with new taint list
	if err := r.Update(context.Background(), node); err != nil {
		loggerTaint.Error(err, "Failed to append taint on node", "node name", node.Name, "taint key", taint.Key, "taint effect", taint.Effect)
		return false, err
	}
	loggerTaint.Info("Taint was added", "taint effect", taint.Effect, "taint list", node.Spec.Taints)
	return true, nil
}

// UpdateBoundTaints uses the caller's verified Node and resourceVersion without name-based rebinding.
func UpdateBoundTaints(ctx context.Context, writer client.Writer, node *corev1.Node, binding v1alpha1.NodeBinding, add, remove []corev1.Taint) error {
	if err := binding.Matches(node); err != nil {
		return err
	}
	if node.ResourceVersion == "" {
		return fmt.Errorf("node resourceVersion required for conditional taint update")
	}
	changed := false
	for _, taint := range remove {
		var removed bool
		node.Spec.Taints, removed = deleteTaint(node.Spec.Taints, &taint)
		changed = changed || removed
	}
	for _, taint := range add {
		if !TaintExists(node.Spec.Taints, &taint) {
			node.Spec.Taints = append(node.Spec.Taints, taint)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	if err := writer.Update(ctx, node); err != nil {
		return fmt.Errorf("update identity-bound Node taints: %w", err)
	}
	return nil
}

// RemoveTaint removes taint from the taint list when it is existed, and returns error if it fails in the process
func RemoveTaint(r client.Client, nodeName string, taint corev1.Taint) error {
	// find node by name
	node, err := GetNodeWithName(r, nodeName)
	if node == nil {
		return err
	}

	// check if taint exist
	if !TaintExists(node.Spec.Taints, &taint) {
		return nil
	}

	// delete the taint from the taint list
	if taints, deleted := deleteTaint(node.Spec.Taints, &taint); !deleted {
		loggerTaint.Info("Failed to remove taint from node - taint was not found", "node name", node.Name, "taint key", taint.Key, "taint effect", taint.Effect)
		return nil
	} else {
		node.Spec.Taints = taints
	}

	// update with new taint list
	if err := r.Update(context.Background(), node); err != nil {
		return err
	}
	loggerTaint.Info("Taint was removed", "taint effect", taint.Effect, "taint list", node.Spec.Taints)
	return nil
}
