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

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-logr/logr"

	apiErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/medik8s/fence-agents-remediation/api/v1alpha1"
	"github.com/medik8s/fence-agents-remediation/pkg/cli"
	"github.com/medik8s/fence-agents-remediation/pkg/utils"
)

const (
	errorMissingParams     = "nodeParameters or sharedParameters or both are missing, and they cannot be empty"
	errorMissingNodeParams = "node parameter is required, and cannot be empty"
	SuccessFAResponse      = "Success: Rebooted"
	parameterActionName    = "--action"
	parameterActionValue   = "reboot"
)

// FenceAgentsRemediationReconciler reconciles a FenceAgentsRemediation object
type FenceAgentsRemediationReconciler struct {
	client.Client
	Log      logr.Logger
	Scheme   *runtime.Scheme
	Executor cli.Executer
}

// SetupWithManager sets up the controller with the Manager.
func (r *FenceAgentsRemediationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.FenceAgentsRemediation{}).
		Complete(r)
}

//+kubebuilder:rbac:groups=storage.k8s.io,resources=volumeattachments,verbs=get;list;watch;delete
//+kubebuilder:rbac:groups=core,resources=pods/exec,verbs=create
//+kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;update;delete;deletecollection
//+kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch;update;delete
//+kubebuilder:rbac:groups=fence-agents-remediation.medik8s.io,resources=fenceagentsremediations,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=fence-agents-remediation.medik8s.io,resources=fenceagentsremediations/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=fence-agents-remediation.medik8s.io,resources=fenceagentsremediations/finalizers,verbs=update

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
	r.Log.Info("Begin FenceAgentsRemediation Reconcile")
	defer r.Log.Info("Finish FenceAgentsRemediation Reconcile")
	emptyResult := ctrl.Result{}

	// Fetch the FenceAgentsRemediation instance
	far := &v1alpha1.FenceAgentsRemediation{}
	if err := r.Get(ctx, req.NamespacedName, far); err != nil {
		if apiErrors.IsNotFound(err) {
			// FenceAgentsRemediation CR was not found, and it could have been deleted after reconcile request.
			// Return and don't requeue
			r.Log.Info("FenceAgentsRemediation CR was not found", "CR Name", req.Name, "CR Namespace", req.Namespace)
			return emptyResult, nil
		}
		r.Log.Error(err, "Failed to get FenceAgentsRemediation CR")
		return emptyResult, err
	}
	// Validate FAR CR name to match a nodeName from the cluster
	r.Log.Info("Check FAR CR's name")
	valid, err := utils.IsNodeNameValid(r.Client, req.Name)
	if err != nil {
		r.Log.Error(err, "Unexpected error when validating CR's name with nodes' names", "CR's Name", req.Name)
		return emptyResult, err
	}
	if !valid {
		r.Log.Error(err, "Didn't find a node matching the CR's name", "CR's Name", req.Name)
		return emptyResult, nil
	}

	// Add finalizer when the CR is created
	if !controllerutil.ContainsFinalizer(far, v1alpha1.FARFinalizer) && far.ObjectMeta.DeletionTimestamp.IsZero() {
		controllerutil.AddFinalizer(far, v1alpha1.FARFinalizer)
		if err := r.Client.Update(context.Background(), far); err != nil {
			return emptyResult, fmt.Errorf("failed to add finalizer to the CR - %w", err)
		}
		r.Log.Info("Finalizer was added", "CR Name", req.Name)
		return emptyResult, nil
		// TODO: should return Requeue: true when the CR has a status and not end reconcile with empty result when a finalizer has been added
		//return ctrl.Result{Requeue: true}, nil
	} else if controllerutil.ContainsFinalizer(far, v1alpha1.FARFinalizer) && !far.ObjectMeta.DeletionTimestamp.IsZero() {
		// Delete CR only when a finalizer and DeletionTimestamp are set
		r.Log.Info("CR's deletion timestamp is not zero, and FAR finalizer exists", "CR Name", req.Name)
		// remove node's taints
		if err := utils.RemoveTaint(r.Client, far.Name); err != nil && !apiErrors.IsNotFound(err) {
			return emptyResult, err
		}
		// remove finalizer
		controllerutil.RemoveFinalizer(far, v1alpha1.FARFinalizer)
		if err := r.Client.Update(context.Background(), far); err != nil {
			return emptyResult, fmt.Errorf("failed to remove finalizer from CR - %w", err)
		}
		r.Log.Info("Finalizer was removed", "CR Name", req.Name)
		return emptyResult, nil
	}

	// Fetch the FAR's pod
	r.Log.Info("Fetch FAR's pod")
	pod, err := utils.GetFenceAgentsRemediationPod(r.Client)
	if err != nil {
		r.Log.Error(err, "Can't find FAR's pod by its label", "CR's Name", req.Name)
		return emptyResult, err
	}
	//TODO: Check that FA is excutable? run cli.IsExecuteable

	// Build FA parameters
	r.Log.Info("Combine fence agent parameters", "Fence Agent", far.Spec.Agent, "Node Name", req.Name)
	faParams, err := buildFenceAgentParams(far)
	if err != nil {
		r.Log.Error(err, "Invalid sharedParameters/nodeParameters from CR - edit/recreate the CR", "CR's Name", req.Name)
		return emptyResult, nil
	}
	// Add medik8s remediation taint
	r.Log.Info("Add Medik8s remediation taint", "Fence Agent", far.Spec.Agent, "Node Name", req.Name)
	if err := utils.AppendTaint(r.Client, far.Name); err != nil {
		return emptyResult, err
	}
	cmd := append([]string{far.Spec.Agent}, faParams...)
	// The Fence Agent is excutable and the parameters structure are valid, but we don't check their values
	r.Log.Info("Execute the fence agent", "Fence Agent", far.Spec.Agent, "Node Name", req.Name)
	outputRes, outputErr, err := r.Executor.Execute(pod, cmd)
	if err != nil {
		// response was a failure message
		r.Log.Error(err, "Fence Agent response was a failure", "CR's Name", req.Name)
		return emptyResult, err
	}
	if outputErr != "" || outputRes != SuccessFAResponse+"\n" {
		// response wasn't failure or sucesss message
		err := fmt.Errorf("unknown fence agent response - expecting `%s` response, but we received `%s`", SuccessFAResponse, outputRes)
		r.Log.Error(err, "Fence Agent response wasn't a success message", "CR's Name", req.Name)
		return emptyResult, err
	}
	r.Log.Info("Fence Agent command was finished successfully", "Fence Agent", far.Spec.Agent, "Node name", req.Name, "Response", SuccessFAResponse)

	// Reboot was finished and now we remove workloads (pods and their VA)
	r.Log.Info("Manual workload deletion", "Fence Agent", far.Spec.Agent, "Node Name", req.Name)
	if err := utils.DeleteResources(ctx, r.Client, req.Name); err != nil {
		r.Log.Error(err, "Manual workload deletion has failed", "CR's Name", req.Name)
		return emptyResult, err
	}

	return emptyResult, nil
}

// buildFenceAgentParams collects the FAR's parameters for the node based on FAR CR, and if the CR is missing parameters
// or the CR's name don't match nodeParamter name or it has an action which is different than reboot, then return an error
func buildFenceAgentParams(far *v1alpha1.FenceAgentsRemediation) ([]string, error) {
	logger := ctrl.Log.WithName("build-fa-parameters")
	if far.Spec.NodeParameters == nil || far.Spec.SharedParameters == nil {
		err := errors.New(errorMissingParams)
		logger.Error(err, "Missing parameters")
		return nil, err
	}
	var fenceAgentParams []string
	// add shared parameters except the action parameter
	for paramName, paramVal := range far.Spec.SharedParameters {
		if paramName != parameterActionName {
			fenceAgentParams = appendParamToSlice(fenceAgentParams, paramName, paramVal)
		} else if paramVal != parameterActionValue {
			// --action attribute was selected but it is differemt than reboot
			err := errors.New("FAR doesn't support any other action than reboot")
			logger.Error(err, "can't build CR with this action attribute", "action", paramVal)
			return nil, err
		}
	}
	// if --action attribute was not selected, then its default value is reboot
	// https://github.com/ClusterLabs/fence-agents/blob/main/lib/fencing.py.py#L103
	// Therefore we can safely add the reboot action regardless if it was initially added into the CR
	fenceAgentParams = appendParamToSlice(fenceAgentParams, parameterActionName, parameterActionValue)

	// append node parameters
	nodeName := v1alpha1.NodeName(far.Name)
	for paramName, nodeMap := range far.Spec.NodeParameters {
		if nodeVal, isFound := nodeMap[nodeName]; isFound {
			fenceAgentParams = appendParamToSlice(fenceAgentParams, paramName, nodeVal)
		} else {
			err := errors.New(errorMissingNodeParams)
			logger.Error(err, "Missing matching nodeParam and CR's name")
			return nil, err
		}
	}
	return fenceAgentParams, nil
}

// appendParamToSlice appends parameters in a key-value manner, when value can be empty
func appendParamToSlice(fenceAgentParams []string, paramName v1alpha1.ParameterName, paramVal string) []string {
	if paramVal != "" {
		fenceAgentParams = append(fenceAgentParams, fmt.Sprintf("%s=%s", paramName, paramVal))
	} else {
		fenceAgentParams = append(fenceAgentParams, string(paramName))
	}
	return fenceAgentParams
}
