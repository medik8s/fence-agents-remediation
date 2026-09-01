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

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	remediationv1alpha1 "github.com/medik8s/fence-agents-remediation/v5/api/v1alpha1"
)

var (
	// webhookFARLog is for logging in this package.
	webhookFARLog = logf.Log.WithName("fenceagentsremediation-resource")
)

func SetupFARWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &remediationv1alpha1.FenceAgentsRemediation{}).
		WithValidator(&remediationv1alpha1.FARValidator{
			Client: mgr.GetClient(),
		}).
		WithDefaulter(&farDefaulter{
			Client: mgr.GetClient(),
		}).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-fence-agents-remediation-medik8s-io-v1alpha1-fenceagentsremediation,mutating=true,failurePolicy=fail,sideEffects=None,groups=fence-agents-remediation.medik8s.io,resources=fenceagentsremediations,verbs=create;update,versions=v1alpha1,name=mfenceagentsremediation.kb.io,admissionReviewVersions=v1
// +kubebuilder:webhook:path=/validate-fence-agents-remediation-medik8s-io-v1alpha1-fenceagentsremediation,mutating=false,failurePolicy=fail,sideEffects=None,groups=fence-agents-remediation.medik8s.io,resources=fenceagentsremediations,verbs=create;update,versions=v1alpha1,name=vfenceagentsremediation.kb.io,admissionReviewVersions=v1

type farDefaulter struct {
	client.Client
}

var _ admission.Defaulter[*remediationv1alpha1.FenceAgentsRemediation] = &farDefaulter{}

func (d *farDefaulter) Default(ctx context.Context, far *remediationv1alpha1.FenceAgentsRemediation) error {
	webhookFARLog.Info("default", "name", far.Name)
	isCreate := far.CreationTimestamp.IsZero()
	return applySharedSecretDefaultNameToSpec(ctx, d.Client, &far.Spec, far.Namespace, isCreate)
}
