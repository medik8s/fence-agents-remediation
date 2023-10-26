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
	"time"

	"github.com/go-logr/logr"
	commonAnnotations "github.com/medik8s/common/pkg/annotations"
	commonConditions "github.com/medik8s/common/pkg/conditions"

	apiErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilErrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/medik8s/fence-agents-remediation/api/v1alpha1"
	"github.com/medik8s/fence-agents-remediation/pkg/cli"
	"github.com/medik8s/fence-agents-remediation/pkg/utils"
)

const (
	// errors
	errorMissingParams     = "nodeParameters or sharedParameters or both are missing, and they cannot be empty"
	errorMissingNodeParams = "node parameter is required, and cannot be empty"

	SuccessFAResponse    = "Success: Rebooted"
	parameterActionName  = "--action"
	parameterActionValue = "reboot"
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
//+kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch
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
func (r *FenceAgentsRemediationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (finalResult ctrl.Result, finalErr error) {
	r.Log.Info("Begin FenceAgentsRemediation Reconcile")
	defer r.Log.Info("Finish FenceAgentsRemediation Reconcile")
	// Reconcile requeue results
	emptyResult := ctrl.Result{}
	requeueImmediately := ctrl.Result{Requeue: true}

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

	// At the end of each Reconcile we try to update CR's status
	defer func() {
		if updateErr := r.updateStatus(ctx, far); updateErr != nil {
			if apiErrors.IsConflict(updateErr) {
				r.Log.Info("Conflict has occurred on updating the CR status")
			}
			finalErr = utilErrors.NewAggregate([]error{updateErr, finalErr})
		}
	}()

	// Validate FAR CR name to match a nodeName from the cluster
	r.Log.Info("Check FAR CR's name")
	valid, err := utils.IsNodeNameValid(r.Client, req.Name)
	if err != nil {
		r.Log.Error(err, "Unexpected error when validating CR's name with nodes' names", "CR's Name", req.Name)
		return emptyResult, err
	}
	if !valid {
		r.Log.Error(err, "Didn't find a node matching the CR's name", "CR's Name", req.Name)
		err := updateConditions(v1alpha1.RemediationFinishedNodeNotFound, far, r.Log)
		return emptyResult, err
	}

	// Check NHC timeout annotation
	if isTimedOutByNHC(far) {
		r.Log.Info("FAR remediation was stopped by Node Healthcheck Operator")
		err := updateConditions(v1alpha1.RemediationInterruptedByNHC, far, r.Log)
		return emptyResult, err
	}

	// Add finalizer when the CR is created
	if !controllerutil.ContainsFinalizer(far, v1alpha1.FARFinalizer) && far.ObjectMeta.DeletionTimestamp.IsZero() {
		controllerutil.AddFinalizer(far, v1alpha1.FARFinalizer)
		if err := r.Client.Update(context.Background(), far); err != nil {
			return emptyResult, fmt.Errorf("failed to add finalizer to the CR - %w", err)
		}
		r.Log.Info("Finalizer was added", "CR Name", req.Name)

		if err := updateConditions(v1alpha1.RemediationStarted, far, r.Log); err != nil {
			return emptyResult, err
		}
		return requeueImmediately, nil
	} else if controllerutil.ContainsFinalizer(far, v1alpha1.FARFinalizer) && !far.ObjectMeta.DeletionTimestamp.IsZero() {
		// Delete CR only when a finalizer and DeletionTimestamp are set
		r.Log.Info("CR's deletion timestamp is not zero, and FAR finalizer exists", "CR Name", req.Name)

		if !meta.IsStatusConditionPresentAndEqual(far.Status.Conditions, commonConditions.SucceededType, metav1.ConditionTrue) {
			processingCondition := meta.FindStatusCondition(far.Status.Conditions, commonConditions.ProcessingType).Status
			fenceAgentActionSucceededCondition := meta.FindStatusCondition(far.Status.Conditions, v1alpha1.FenceAgentActionSucceededType).Status
			succeededCondition := meta.FindStatusCondition(far.Status.Conditions, commonConditions.SucceededType).Status
			r.Log.Info("FAR didn't finish remediate the node ", "CR Name", req.Name, "processing condition", processingCondition,
				"fenceAgentActionSucceeded condition", fenceAgentActionSucceededCondition, "succeeded condition", succeededCondition)
		}

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
	// Add FAR (medik8s) remediation taint
	r.Log.Info("Try adding FAR (Medik8s) remediation taint", "Fence Agent", far.Spec.Agent, "Node Name", req.Name)
	if err := utils.AppendTaint(r.Client, far.Name); err != nil {
		return emptyResult, err
	}

	if meta.IsStatusConditionTrue(far.Status.Conditions, commonConditions.ProcessingType) &&
		!meta.IsStatusConditionTrue(far.Status.Conditions, v1alpha1.FenceAgentActionSucceededType) {
		// The remeditation has already been processed, thus we can begin with exuecting the FA for the node
		// We run the FA until its action (reboot) was succeeded, and we verify it with the fenceAgentActionSucceeded condition

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
		if err := updateConditions(v1alpha1.FenceAgentSucceeded, far, r.Log); err != nil {
			return emptyResult, err
		}
		return requeueImmediately, nil
	}
	if meta.IsStatusConditionTrue(far.Status.Conditions, v1alpha1.FenceAgentActionSucceededType) &&
		!meta.IsStatusConditionTrue(far.Status.Conditions, commonConditions.SucceededType) {
		// Fence agent action succeeded, and now we try to remove workloads (pods and their volume attachments)
		r.Log.Info("Manual workload deletion", "Fence Agent", far.Spec.Agent, "Node Name", req.Name)
		if err := utils.DeleteResources(ctx, r.Client, req.Name); err != nil {
			r.Log.Error(err, "Manual workload deletion has failed", "CR's Name", req.Name)
			return emptyResult, err
		}
		if err := updateConditions(v1alpha1.RemediationFinishedSuccessfully, far, r.Log); err != nil {
			return emptyResult, err
		}
		r.Log.Info("FenceAgentsRemediation CR has completed to remediate the node", "Node Name", req.Name)
	}

	return emptyResult, nil
}

// isTimedOutByNHC checks if NHC set a timeout annotation on the CR
func isTimedOutByNHC(far *v1alpha1.FenceAgentsRemediation) bool {
	if far != nil && far.Annotations != nil && far.DeletionTimestamp == nil {
		_, isTimeoutIssued := far.Annotations[commonAnnotations.NhcTimedOut]
		return isTimeoutIssued
	}
	return false
}

// updateStatus updates the CR status, and returns an error if it fails
func (r *FenceAgentsRemediationReconciler) updateStatus(ctx context.Context, far *v1alpha1.FenceAgentsRemediation) error {
	// When CR doesn't include a finalizer and the CR deletionTimestamp exsists
	// then we can skip update, since it will be removed soon.
	if !controllerutil.ContainsFinalizer(far, v1alpha1.FARFinalizer) && !far.ObjectMeta.DeletionTimestamp.IsZero() {
		return nil
	}
	if err := r.Client.Status().Update(ctx, far); err != nil {
		if !apiErrors.IsConflict(err) {
			r.Log.Error(err, "failed to update far status in case on a non conflict")
		}
		return err
	}
	// Wait until the cache is updated in order to prevent reading a stale status in the next reconcile
	// and making wrong decisions based on it.
	pollingTimeout := 5 * time.Second
	pollErr := wait.PollUntilContextTimeout(ctx, 200*time.Millisecond, pollingTimeout, true, func(ctx context.Context) (bool, error) {
		tmpFar := &v1alpha1.FenceAgentsRemediation{}
		if err := r.Client.Get(ctx, client.ObjectKeyFromObject(far), tmpFar); err != nil {
			if apiErrors.IsNotFound(err) {
				// nothing we can do anymore
				return true, nil
			}
			return false, nil
		}
		return tmpFar.Status.LastUpdateTime != nil && (tmpFar.Status.LastUpdateTime.Equal(far.Status.LastUpdateTime) || tmpFar.Status.LastUpdateTime.After(far.Status.LastUpdateTime.Time)), nil
	})
	if pollErr != nil {
		return fmt.Errorf("failed to wait for updated cache to be updated in status update after %f seconds of timeout - %w", pollingTimeout.Seconds(), pollErr)
	}
	return nil
}

// updateConditions updates the status conditions of a FenceAgentsRemediation object based on the provided ConditionsChangeReason.
// return an error if an unknown ConditionsChangeReason is provided
func updateConditions(reason v1alpha1.ConditionsChangeReason, far *v1alpha1.FenceAgentsRemediation, log logr.Logger) error {

	var (
		processingConditionStatus, fenceAgentActionSucceededConditionStatus, succeededConditionStatus metav1.ConditionStatus
		conditionMessage                                                                              string
	)
	currentConditions := &far.Status.Conditions
	conditionHasBeenChanged := false

	// RemediationFinishedNodeNotFound and RemediationInterruptedByNHC reasons can happen at any time the Reconcile runs
	// Except these two reasons, there are another three reasons that can only happen one after another
	// RemediationStarted will always be the first reason (out of these three)
	// FenceAgentSucceeded can only happen after RemediationStarted happened
	// RemediationFinishedSuccessfully can only happen after FenceAgentSucceeded happened
	switch reason {
	case v1alpha1.RemediationFinishedNodeNotFound, v1alpha1.RemediationInterruptedByNHC:
		processingConditionStatus = metav1.ConditionFalse
		fenceAgentActionSucceededConditionStatus = metav1.ConditionFalse
		succeededConditionStatus = metav1.ConditionFalse
		// Different reasons share the same effect to the conditions, but they have different message
		switch reason {
		case v1alpha1.RemediationFinishedNodeNotFound:
			conditionMessage = v1alpha1.RemediationFinishedNodeNotFoundConditionMessage
		case v1alpha1.RemediationInterruptedByNHC:
			conditionMessage = v1alpha1.RemediationInterruptedByNHCConditionMessage
		default:
			err := fmt.Errorf("unknown ConditionsChangeReason:%s", reason)
			log.Error(err, "couldn't update FAR Status Conditions")
			return err
		}
	case v1alpha1.RemediationStarted:
		processingConditionStatus = metav1.ConditionTrue
		fenceAgentActionSucceededConditionStatus = metav1.ConditionUnknown
		succeededConditionStatus = metav1.ConditionUnknown
		conditionMessage = v1alpha1.RemediationStartedConditionMessage
	case v1alpha1.FenceAgentSucceeded:
		fenceAgentActionSucceededConditionStatus = metav1.ConditionTrue
		conditionMessage = v1alpha1.FenceAgentSucceededConditionMessage
	case v1alpha1.RemediationFinishedSuccessfully:
		processingConditionStatus = metav1.ConditionFalse
		succeededConditionStatus = metav1.ConditionTrue
		conditionMessage = v1alpha1.RemediationFinishedSuccessfullyConditionMessage
	default:
		err := fmt.Errorf("unknown ConditionsChangeReason:%s", reason)
		log.Error(err, "couldn't update FAR Status Conditions")
		return err
	}

	// if the requested Status.Conditions.Processing is different then the current one, then update Status.Conditions.Processing value
	if processingConditionStatus != "" && !meta.IsStatusConditionPresentAndEqual(*currentConditions, commonConditions.ProcessingType, processingConditionStatus) {
		meta.SetStatusCondition(currentConditions, metav1.Condition{
			Type:    commonConditions.ProcessingType,
			Status:  processingConditionStatus,
			Reason:  string(reason),
			Message: conditionMessage,
		})
		conditionHasBeenChanged = true
	}

	// if the requested Status.Conditions.FenceAgentActionSucceeded is different then the current one, then update Status.Conditions.FenceAgentActionSucceeded value
	if fenceAgentActionSucceededConditionStatus != "" && !meta.IsStatusConditionPresentAndEqual(*currentConditions, v1alpha1.FenceAgentActionSucceededType, fenceAgentActionSucceededConditionStatus) {
		meta.SetStatusCondition(currentConditions, metav1.Condition{
			Type:    v1alpha1.FenceAgentActionSucceededType,
			Status:  fenceAgentActionSucceededConditionStatus,
			Reason:  string(reason),
			Message: conditionMessage,
		})
		conditionHasBeenChanged = true
	}

	// if the requested Status.Conditions.Succeeded is different then the current one, then update Status.Conditions.Succeeded value
	if succeededConditionStatus != "" && !meta.IsStatusConditionPresentAndEqual(*currentConditions, commonConditions.SucceededType, succeededConditionStatus) {
		meta.SetStatusCondition(currentConditions, metav1.Condition{
			Type:    commonConditions.SucceededType,
			Status:  succeededConditionStatus,
			Reason:  string(reason),
			Message: conditionMessage,
		})
		conditionHasBeenChanged = true
	}
	// Only update lastUpdate when there were other changes
	if conditionHasBeenChanged {
		now := metav1.Now()
		far.Status.LastUpdateTime = &now
	}
	log.Info("Updating Status Condition", "processingConditionStatus", processingConditionStatus, "fenceAgentActionSucceededConditionStatus", fenceAgentActionSucceededConditionStatus, "succededConditionStatus", succeededConditionStatus, "reason", string(reason), "LastUpdateTime", far.Status.LastUpdateTime)

	return nil
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
