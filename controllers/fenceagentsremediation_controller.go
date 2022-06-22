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

package controllers

//TODO mshitrit make sure fence agents and other necessary executables are installed in the pod

import (
	"bytes"
	"context"
	"fmt"
	"github.com/go-logr/logr"
	"github.com/mshitrit/fence-agents/api/v1alpha1"
	"github.com/mshitrit/fence-agents/pkg/cli"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apiErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"net/http"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	//TODO mshitrit verify that template is created with this name
	fenceAgentsTemplateName = "fenceagentsremediationtemplate-default"
)

var (
	//TODO mshitrit plant the label on the pod
	faPodLabels = map[string]string{"app": "fence-agent-operator"}
)

// FenceAgentsRemediationReconciler reconciles a FenceAgentsRemediation object
type FenceAgentsRemediationReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=core,resources=pods/exec,verbs=create
//+kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;update;delete;deletecollection
//+kubebuilder:rbac:groups=fence-agents.medik8s.io,resources=fenceagentsremediationtemplates,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=fence-agents.medik8s.io,resources=fenceagentsremediations,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=fence-agents.medik8s.io,resources=fenceagentsremediations/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=fence-agents.medik8s.io,resources=fenceagentsremediations/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the FenceAgentsRemediation object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.11.2/pkg/reconcile
func (r *FenceAgentsRemediationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {

	far := &v1alpha1.FenceAgentsRemediation{}
	if err := r.Get(ctx, req.NamespacedName, far); err != nil {
		if apiErrors.IsNotFound(err) {
			// FAR is deleted, stop reconciling
			r.Log.Info("Fence Agents Remediation is already deleted")
			return ctrl.Result{}, nil
		}
		r.Log.Error(err, "failed to get FAR")
		return ctrl.Result{}, err
	}
	key := client.ObjectKey{Namespace: req.Namespace, Name: fenceAgentsTemplateName}
	farTemplate := &v1alpha1.FenceAgentsRemediationTemplate{}
	if err := r.Get(ctx, key, farTemplate); err != nil {
		r.Log.Error(err, "failed to get FAR template")
		return ctrl.Result{}, err
	}

	pod, err := r.getFAPod(req.NamespacedName.Namespace)
	if err != nil {
		return ctrl.Result{}, err
	}

	ex, err := cli.NewExecuter(pod)
	if err != nil {
		return ctrl.Result{}, err
	}

	faParams := buildFenceAgentParamFile(farTemplate, far)
	cmd := []string{"echo", "-e", faParams.String(), "|", farTemplate.Spec.Agent}
	//echo -e "params" | fence_ipmilan
	if _, _, err := ex.Execute(cmd); err != nil {
		return ctrl.Result{}, err
	}

	//Use the file to trigger fencing
	//cat param.file | fence_ipmilan

	return ctrl.Result{}, nil
}

func buildFenceAgentParamFile(farTemplate *v1alpha1.FenceAgentsRemediationTemplate, far *v1alpha1.FenceAgentsRemediation) bytes.Buffer {
	var fenceAgentParams bytes.Buffer
	for paramName, paramVal := range farTemplate.Spec.SharedParameters {
		fenceAgentParams.WriteString(fmt.Sprintf("%s=%s\n", paramName, paramVal))
	}

	nodeName := v1alpha1.NodeName(far.Name)
	for paramName, nodeMap := range farTemplate.Spec.NodeParameters {
		fenceAgentParams.WriteString(fmt.Sprintf("%s=%s\n", paramName, nodeMap[nodeName]))
	}

	return fenceAgentParams
}

// SetupWithManager sets up the controller with the Manager.
func (r *FenceAgentsRemediationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.FenceAgentsRemediation{}).
		Complete(r)
}

func (r *FenceAgentsRemediationReconciler) getFAPod(namespace string) (*corev1.Pod, error) {

	pods := new(corev1.PodList)

	podLabelsSelector, _ := metav1.LabelSelectorAsSelector(
		&metav1.LabelSelector{MatchLabels: faPodLabels})
	options := client.ListOptions{
		LabelSelector: podLabelsSelector,
		Namespace:     namespace,
	}
	if err := r.Client.List(context.Background(), pods, &options); err != nil {
		r.Log.Error(err, "failed fetching Fence Agent layer pod")
		return nil, err
	}
	if len(pods.Items) == 0 {
		r.Log.Info("No Fence Agent pods were found")
		podNotFoundErr := &errors.StatusError{ErrStatus: metav1.Status{
			Status: metav1.StatusFailure,
			Code:   http.StatusNotFound,
			Reason: metav1.StatusReasonNotFound,
		}}
		return nil, podNotFoundErr
	}
	return &pods.Items[0], nil

}
