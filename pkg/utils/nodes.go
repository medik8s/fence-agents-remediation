package utils

import (
	"context"

	medik8sLabels "github.com/medik8s/common/pkg/labels"

	corev1 "k8s.io/api/core/v1"
	apiErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// GetNodeWithName returns a node with a name nodeName, or an error if it can't be found
func GetNodeWithName(r client.Reader, nodeName string) (*corev1.Node, error) {
	node := &corev1.Node{}
	key := client.ObjectKey{Name: nodeName}
	if err := r.Get(context.TODO(), key, node); err != nil {
		return nil, err
	}
	return node, nil
}

// IsNodeNameValid returns an error if nodeName doesn't match any node name int the cluster, otherwise a nil
func IsNodeNameValid(r client.Reader, nodeName string) (*corev1.Node, bool, error) {
	node, err := GetNodeWithName(r, nodeName)
	if err != nil {
		if apiErrors.IsNotFound(err) {
			// In case of notFound API error we don't return error, since it is valid result
			return node, false, nil
		} else {
			return node, false, err
		}
	}
	return node, true, nil
}

// GetNode returns a node object with the name nodeName based on the nodeType input
// used for making new node object for test and have a unique resourceVersion
func GetNode(nodeRole, nodeName string) *corev1.Node {
	if nodeRole == "control-plane" {
		return &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: nodeName,
			},
			Spec: corev1.NodeSpec{
				Taints: []corev1.Taint{
					{
						Key:    medik8sLabels.ControlPlaneRole,
						Effect: corev1.TaintEffectNoExecute,
					},
				},
			},
		}
	} else {
		return &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: nodeName,
			},
		}
	}
}
