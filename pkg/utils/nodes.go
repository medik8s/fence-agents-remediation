package utils

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CheckNodeName returns an error if nodeName doesn't match any node name int the cluster, otherwise a nil
func CheckNodeName(r client.Reader, nodeName string) error {
	node := &corev1.Node{}
	objNodeName := types.NamespacedName{Name: nodeName}
	err := r.Get(context.Background(), objNodeName, node)
	if err != nil {
		// no match between CR name and the cluster nodes
		msg := fmt.Sprintf("node %s is invalid", nodeName)
		err = buildApiError(err, msg)
		return fmt.Errorf("CR's name doesn't match any node name in the cluster - %w", err)
	}
	return nil
}
