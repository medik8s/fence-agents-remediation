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

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var (
	// isOutOfServiceTaintSupported will be set to true in case out-of-service taint is supported (k8s 1.26 or higher)
	isOutOfServiceTaintSupported bool

	// webhookFARLog is for logging in this package.
	webhookFARLog = logf.Log.WithName("fenceagentsremediation-resource")
)

func (r *FenceAgentsRemediation) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		WithValidator(&customValidator{
			Client: mgr.GetClient(),
		}).
		WithDefaulter(&farDefaulter{
			Client: mgr.GetClient(),
		}).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-fence-agents-remediation-medik8s-io-v1alpha1-fenceagentsremediation,mutating=true,failurePolicy=fail,sideEffects=None,groups=fence-agents-remediation.medik8s.io,resources=fenceagentsremediations,verbs=create;update,versions=v1alpha1,name=mfenceagentsremediation.kb.io,admissionReviewVersions=v1

type farDefaulter struct {
	client.Client
}

var _ admission.CustomDefaulter = &farDefaulter{}

// Default implements webhook.Defaulter so a webhook will be registered for the type
func (d *farDefaulter) Default(ctx context.Context, obj runtime.Object) error {
	far, ok := obj.(*FenceAgentsRemediation)
	if !ok {
		return fmt.Errorf("expected a FenceAgentsRemediation but got %T", obj)
	}
	webhookFARLog.Info("default", "name", far.Name)
	return applySharedSecretDefaultNameToSpec(ctx, d.Client, &far.Spec, far.Namespace)
}

func InitOutOfServiceTaintSupportedFlag(outOfServiceTaintSupported bool) {
	isOutOfServiceTaintSupported = outOfServiceTaintSupported
}
