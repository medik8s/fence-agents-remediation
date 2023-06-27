package utils

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	apiErrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const WorkerLabelName = "node-role.kubernetes.io/worker"

// getNodeWithName returns a node with a name nodeName, or an error if it can't be found
func getNodeWithName(r client.Reader, nodeName string) (*corev1.Node, error) {
	node := &corev1.Node{}
	key := client.ObjectKey{Name: nodeName}
	if err := r.Get(context.TODO(), key, node); err != nil {
		return nil, err
	}
	return node, nil
}

// IsNodeNameValid returns an error if nodeName doesn't match any node name int the cluster, otherwise a nil
func IsNodeNameValid(r client.Reader, nodeName string) (bool, error) {
	_, err := getNodeWithName(r, nodeName)
	if err != nil {
		if apiErrors.IsNotFound(err) {
			// In case of notFound API error we don't return error, since it is valid result
			return false, nil
		} else {
			return false, err
		}
	}
	return true, nil
}
