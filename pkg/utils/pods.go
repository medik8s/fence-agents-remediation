package utils

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// GetFenceAgentsRemediationPod fetches the first running pod that matches to FAR's label and namespace
// The pod should be on running state since when we taint and reboot a node which had FAR, then that pod will be restarted on a different node,
// This results with an old pod which is about to die and new pod is already running and we want to return the running pod
func GetFenceAgentsRemediationPod(r client.Reader) (*corev1.Pod, error) {
	podList := &corev1.PodList{}
	selector := labels.NewSelector()
	requirement, _ := labels.NewRequirement("app.kubernetes.io/name", selection.Equals, []string{"fence-agents-remediation-operator"})
	selector = selector.Add(*requirement)
	podNamespace, err := GetDeploymentNamespace()
	if err != nil {
		return nil, fmt.Errorf("failed fetching FAR namespace - %w", err)
	}
	err = r.List(context.Background(), podList, &client.ListOptions{LabelSelector: selector, Namespace: podNamespace})
	if err != nil {
		return nil, fmt.Errorf("failed fetching FAR pod - %w", err)
	}
	if len(podList.Items) == 0 {
		return nil, fmt.Errorf("no FAR pods were found")
	}
	for _, pod := range podList.Items {
		if pod.Status.Phase == corev1.PodRunning {
			return &pod, nil
		}
	}
	return nil, fmt.Errorf("no running FAR pods were found")
}
