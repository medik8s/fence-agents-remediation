package utils

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	apiErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// IsNodeNameValid returns an error if nodeName doesn't match any node name int the cluster, otherwise a nil
func IsNodeNameValid(r client.Reader, nodeName string) (bool, error) {
	node := &corev1.Node{}
	objNodeName := types.NamespacedName{Name: nodeName}
	err := r.Get(context.Background(), objNodeName, node)
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
