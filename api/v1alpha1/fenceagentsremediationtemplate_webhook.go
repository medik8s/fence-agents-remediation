/*
Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	"context"
	"fmt"

	commonAnnotations "github.com/medik8s/common/pkg/annotations"

	corev1 "k8s.io/api/core/v1"
	apiErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var (
	// webhookFARTemplateLog is for logging in this package.
	webhookFARTemplateLog = logf.Log.WithName("fenceagentsremediationtemplate-resource")
)

func (farTemplate *FenceAgentsRemediationTemplate) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(farTemplate).
		WithValidator(&customValidator{
			Client: mgr.GetClient(),
		}).
		WithDefaulter(&farTemplateDefaulter{
			Client: mgr.GetClient(),
		}).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-fence-agents-remediation-medik8s-io-v1alpha1-fenceagentsremediationtemplate,mutating=true,failurePolicy=fail,sideEffects=None,groups=fence-agents-remediation.medik8s.io,resources=fenceagentsremediationtemplates,verbs=create;update,versions=v1alpha1,name=mfenceagentsremediationtemplate.kb.io,admissionReviewVersions=v1

type farTemplateDefaulter struct {
	client.Client
}

var _ admission.CustomDefaulter = &farTemplateDefaulter{}

// Default implements webhook.Defaulter so a webhook will be registered for the type
func (d *farTemplateDefaulter) Default(ctx context.Context, obj runtime.Object) error {
	farTemplate, ok := obj.(*FenceAgentsRemediationTemplate)
	if !ok {
		return fmt.Errorf("expected a FenceAgentsRemediationTemplate but got %T", obj)
	}
	webhookFARTemplateLog.Info("default", "name", farTemplate.Name)
	if farTemplate.GetAnnotations() == nil {
		farTemplate.Annotations = make(map[string]string)
	}
	if _, isSameKindAnnotationSet := farTemplate.GetAnnotations()[commonAnnotations.MultipleTemplatesSupportedAnnotation]; !isSameKindAnnotationSet {
		farTemplate.Annotations[commonAnnotations.MultipleTemplatesSupportedAnnotation] = "true"
	}
	isCreate := farTemplate.CreationTimestamp.IsZero()
	if err := applySharedSecretDefaultNameToSpec(ctx, d.Client, &farTemplate.Spec.Template.Spec, farTemplate.Namespace, isCreate); err != nil {
		return err
	}
	return nil
}

// applySharedSecretDefaultNameToSpec applies a workaround for the shared secret name default value:
// - in the first version of FAR which introduced the usage of secrets, we added the new API field "SharedSecretName"
// - like every new API field, it has to be optional, to be backwards compatible
// - however, we also set a default value "fence-agents-credentials-shared" via API
//
// - that introduced issues:
//   - with that default value there is no chance to correctly validate the field,
//     because we don't know if it was set by the user (meaning the Secret should exist) or not
//   - updates of the default value are challenging and can result in backwards compatibility issues
//
// - because of that we decided to
//   - remove the default value, so the field will stay empty for new CRs when it's empty
//   - however, as a workaround, set the old default value on the CR in code when such a Secret exists
//   - and remove the value on existing CRs when no such Secret exists
//
// TODO: This workaround will be removed in a future version
//
// isCreate indicates whether this is a CREATE (true) or UPDATE (false) operation.
// On CREATE, we set the old default value when the secret exists. On UPDATE, we don't,
// because the user might have explicitly removed it. The validating webhook
// (validateTemplateForSharedSecretDefaultName) will reject the update with a helpful
// error message if the secret still exists.
// On both CREATE and UPDATE, we remove the old default value when the secret doesn't exist.
func applySharedSecretDefaultNameToSpec(ctx context.Context, k8sClient client.Client, spec *FenceAgentsRemediationSpec, namespace string, isCreate bool) error {
	// Check if the secret with the old default name exists
	secret := &corev1.Secret{}
	secretKey := client.ObjectKey{Name: OldDefaultSecretName, Namespace: namespace}
	secretExists := true
	if err := k8sClient.Get(ctx, secretKey, secret); err != nil {
		if !apiErrors.IsNotFound(err) {
			return fmt.Errorf("failed to check for shared secret: %w", err)
		}
		secretExists = false
	}

	if isCreate && spec.SharedSecretName == nil && secretExists {
		// Set the old default value when SharedSecretName is nil and the Secret exists (only on create)
		webhookFARTemplateLog.Info("Setting SharedSecretName to old default value as the secret exists", "secretName", OldDefaultSecretName)
		spec.SharedSecretName = ptr.To(OldDefaultSecretName)
	} else if spec.SharedSecretName != nil && *spec.SharedSecretName == OldDefaultSecretName && !secretExists {
		// Remove the old default value when SharedSecretName equals the old default but the Secret doesn't exist
		webhookFARTemplateLog.Info("Removing SharedSecretName old default value as the secret does not exist", "secretName", OldDefaultSecretName)
		spec.SharedSecretName = nil
	}
	return nil
}
