package utils

import (
	"context"
	"fmt"
	"net/http"

	corev1 "k8s.io/api/core/v1"
	apiErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// GetFenceAgentsRemediationPod fetches the FAR pod based on FAR's label and namespace
func GetFenceAgentsRemediationPod(r client.Reader) (*corev1.Pod, error) {
	pods := &corev1.PodList{}
	selector := labels.NewSelector()
	requirement, _ := labels.NewRequirement("app", selection.Equals, []string{"fence-agents-remediation-operator"})
	selector = selector.Add(*requirement)
	var podNamespace string
	podNamespace, err := GetDeploymentNamespace()
	if err != nil {
		fmt.Printf("failed fetching FAR namespace\n")
	}
	err = r.List(context.Background(), pods, &client.ListOptions{LabelSelector: selector, Namespace: podNamespace})
	if err != nil {
		fmt.Printf("failed fetching FAR pod\n")
		return nil, err
	}
	if len(pods.Items) == 0 {
		fmt.Printf("No Fence Agent pods were found\n")
		podNotFoundErr := &apiErrors.StatusError{ErrStatus: metav1.Status{
			Status: metav1.StatusFailure,
			Code:   http.StatusNotFound,
			Reason: metav1.StatusReasonNotFound,
		}}
		return nil, podNotFoundErr
	}

	return &pods.Items[0], nil
}
