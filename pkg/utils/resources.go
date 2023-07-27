package utils

// Inspired from SNR - https://github.com/medik8s/self-node-remediation/blob/main/controllers/selfnoderemediation_controller.go#L283-L346
import (
	"context"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	log = ctrl.Log.WithName("utils-resource")
)

func DeleteResources(ctx context.Context, r client.Client, nodeName string) error {
	zero := int64(0)
	backgroundDeletePolicy := metav1.DeletePropagationBackground

	deleteOptions := &client.DeleteAllOfOptions{
		ListOptions: client.ListOptions{
			FieldSelector: fields.SelectorFromSet(fields.Set{"spec.nodeName": nodeName}),
			Namespace:     "",
			Limit:         0,
		},
		DeleteOptions: client.DeleteOptions{
			GracePeriodSeconds: &zero,
			PropagationPolicy:  &backgroundDeletePolicy,
		},
	}

	namespaces := corev1.NamespaceList{}
	if err := r.List(ctx, &namespaces); err != nil {
		log.Error(err, "failed to list namespaces", err)
		return err
	}

	log.Info("starting to delete node resources", "node name", nodeName)

	pod := &corev1.Pod{}
	for _, ns := range namespaces.Items {
		deleteOptions.Namespace = ns.Name
		err := r.DeleteAllOf(ctx, pod, deleteOptions)
		if err != nil {
			log.Error(err, "failed to delete pods of unhealthy node", "namespace", ns.Name)
			return err
		}
	}

	volumeAttachments := &storagev1.VolumeAttachmentList{}
	if err := r.List(ctx, volumeAttachments); err != nil {
		log.Error(err, "failed to get volumeAttachments list")
		return err
	}
	forceDeleteOption := &client.DeleteOptions{
		GracePeriodSeconds: &zero,
	}
	for _, va := range volumeAttachments.Items {
		if va.Spec.NodeName == nodeName {
			err := r.Delete(ctx, &va, forceDeleteOption)
			if err != nil {
				log.Error(err, "failed to delete volumeAttachment", "name", va.Name)
				return err
			}
		}
	}

	log.Info("done deleting node resources", "node name", nodeName)

	return nil
}
