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
	"strings"
	"time"

	"github.com/go-logr/logr"
	commonAnnotations "github.com/medik8s/common/pkg/annotations"
	commonConditions "github.com/medik8s/common/pkg/conditions"
	commonEvents "github.com/medik8s/common/pkg/events"
	commonResources "github.com/medik8s/common/pkg/resources"

	corev1 "k8s.io/api/core/v1"
	apiErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilErrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/medik8s/fence-agents-remediation/api/v1alpha1"
	"github.com/medik8s/fence-agents-remediation/pkg/cli"
	"github.com/medik8s/fence-agents-remediation/pkg/utils"
)

const (
	// errors
	errorMissingParams      = "nodeParameters or sharedParameters or both are missing, and they cannot be empty"
	errorMissingNodeParams  = "node parameter is required, and cannot be empty"
	errorFailFetchingSecret = "failed to fetch secret key `%s` from secret `%s` at namespace `%s`: %w"

	SuccessFAResponse    = "Success: Rebooted"
	parameterActionName  = "--action"
	parameterActionValue = "reboot"
	DefaultSecretName    = "defaultSecret"
)

// FenceAgentsRemediationReconciler reconciles a FenceAgentsRemediation object
type FenceAgentsRemediationReconciler struct {
	client.Client
	Log      logr.Logger
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
	Executor *cli.Executer
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
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
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
	node, err := utils.GetNodeWithName(r.Client, getNodeName(far))
	if err != nil {
		r.Log.Error(err, "Unexpected error when validating CR's name with nodes' names", "CR's Name", req.Name)
		return emptyResult, err
	}
	if node == nil {
		r.Log.Error(err, "Could not find CR's target node", "CR's Name", req.Name, "Expected node name", getNodeName(far))
		utils.UpdateConditions(utils.RemediationFinishedNodeNotFound, far, r.Log)
		commonEvents.WarningEvent(r.Recorder, far, utils.EventReasonCrNodeNotFound, utils.EventMessageCrNodeNotFound)
		return emptyResult, err
	}

	// Check NHC timeout annotation
	if isTimedOutByNHC(far) {
		r.Log.Info(utils.EventMessageRemediationStoppedByNHC)
		r.Executor.Remove(far.GetUID())
		utils.UpdateConditions(utils.RemediationInterruptedByNHC, far, r.Log)
		commonEvents.RemediationStoppedByNHC(r.Recorder, far)
		return emptyResult, err
	}

	// Add finalizer when the CR is created
	if !controllerutil.ContainsFinalizer(far, v1alpha1.FARFinalizer) && far.ObjectMeta.DeletionTimestamp.IsZero() {
		controllerutil.AddFinalizer(far, v1alpha1.FARFinalizer)
		if err := r.Client.Update(context.Background(), far); err != nil {
			return emptyResult, fmt.Errorf("failed to add finalizer to the CR - %w", err)
		}
		commonEvents.RemediationStarted(r.Recorder, far)

		r.Log.Info("Finalizer was added", "CR Name", req.Name)
		utils.UpdateConditions(utils.RemediationStarted, far, r.Log)
		commonEvents.NormalEvent(r.Recorder, far, utils.EventReasonAddFinalizer, utils.EventMessageAddFinalizer)
		return requeueImmediately, nil
	} else if controllerutil.ContainsFinalizer(far, v1alpha1.FARFinalizer) && !far.ObjectMeta.DeletionTimestamp.IsZero() {
		// Delete CR only when a finalizer and DeletionTimestamp are set
		r.Log.Info("CR's deletion timestamp is not zero, and FAR finalizer exists", "CR Name", req.Name)

		if !meta.IsStatusConditionPresentAndEqual(far.Status.Conditions, commonConditions.SucceededType, metav1.ConditionTrue) {
			processingCondition := meta.FindStatusCondition(far.Status.Conditions, commonConditions.ProcessingType).Status
			fenceAgentActionSucceededCondition := meta.FindStatusCondition(far.Status.Conditions, utils.FenceAgentActionSucceededType).Status
			succeededCondition := meta.FindStatusCondition(far.Status.Conditions, commonConditions.SucceededType).Status
			r.Log.Info("FAR didn't finish remediate the node ", "CR Name", req.Name, "processing condition", processingCondition,
				"fenceAgentActionSucceeded condition", fenceAgentActionSucceededCondition, "succeeded condition", succeededCondition)
			r.Executor.Remove(far.GetUID())
		}

		// remove out-of-service taint when using OutOfServiceTaint remediation
		if far.Spec.RemediationStrategy == v1alpha1.OutOfServiceTaintRemediationStrategy {
			r.Log.Info("Removing out-of-service taint", "Fence Agent", far.Spec.Agent, "Node Name", node.Name)
			taint := utils.CreateOutOfServiceTaint()
			if err := utils.RemoveTaint(r.Client, node.Name, taint); err != nil {
				if apiErrors.IsConflict(err) {
					r.Log.Error(err, "Failed to remove taint from node due to node update, retrying... ,", "node name", node.Name, "taint key", taint.Key, "taint effect", taint.Effect)
					return ctrl.Result{RequeueAfter: time.Second}, nil
				} else if !apiErrors.IsNotFound(err) {
					r.Log.Error(err, "Failed to remove taint from node,", "node name", node.Name, "taint key", taint.Key, "taint effect", taint.Effect)
					return emptyResult, err
				}
			}
			r.Log.Info("out-of-service taint was removed", "Node Name", req.Name)
			commonEvents.NormalEvent(r.Recorder, node, utils.EventReasonRemoveOutOfServiceTaint, utils.EventMessageRemoveOutOfServiceTaint)
		}

		// remove node's taints
		taint := utils.CreateRemediationTaint()
		if err := utils.RemoveTaint(r.Client, node.Name, taint); err != nil {
			if apiErrors.IsConflict(err) {
				r.Log.Info("Failed to remove taint from node due to node update, retrying... ,", "node name", node.Name, "taint key", taint.Key, "taint effect", taint.Effect)
				return ctrl.Result{RequeueAfter: time.Second}, nil

			} else if !apiErrors.IsNotFound(err) {
				r.Log.Error(err, "Failed to remove taint from node,", "node name", node.Name, "taint key", taint.Key, "taint effect", taint.Effect)
				return emptyResult, err
			}
		}

		r.Log.Info("FAR remediation taint was removed", "Node Name", node.Name)
		commonEvents.NormalEvent(r.Recorder, node, utils.EventReasonRemoveRemediationTaint, utils.EventMessageRemoveRemediationTaint)
		// remove finalizer
		controllerutil.RemoveFinalizer(far, v1alpha1.FARFinalizer)
		if err := r.Client.Update(context.Background(), far); err != nil {
			return emptyResult, fmt.Errorf("failed to remove finalizer from CR - %w", err)
		}
		r.Log.Info("Finalizer was removed", "CR Name", req.Name)
		commonEvents.NormalEvent(r.Recorder, far, utils.EventReasonRemoveFinalizer, utils.EventMessageRemoveFinalizer)
		return emptyResult, nil
	}
	// Add FAR (medik8s) remediation taint
	taintAdded, err := utils.AppendTaint(r.Client, node.Name, utils.CreateRemediationTaint())
	if err != nil {
		return emptyResult, err
	} else if taintAdded {
		r.Log.Info("FAR remediation taint was added", "Node Name", node.Name)
		commonEvents.NormalEvent(r.Recorder, node, utils.EventReasonAddRemediationTaint, utils.EventMessageAddRemediationTaint)
	}

	if meta.IsStatusConditionTrue(far.Status.Conditions, commonConditions.ProcessingType) &&
		!meta.IsStatusConditionTrue(far.Status.Conditions, utils.FenceAgentActionSucceededType) {
		// The remeditation has already been processed, thus we can begin with executing the FA for the node

		if r.Executor.Exists(far.GetUID()) {
			r.Log.Info("A Fence Agent is already running", "Fence Agent", far.Spec.Agent, "Node Name", node.Name, "FAR uid", far.GetUID())
			return emptyResult, nil
		}

		r.Log.Info("Build fence agent command line", "Fence Agent", far.Spec.Agent, "Node Name", node.Name)
		faParams, err := buildFenceAgentParams(far, ctx, r.Client)
		if err != nil {
			r.Log.Error(err, "Invalid credential/shared/node parameter from CR", "Node Name", node.Name, "CR Name", req.Name)
			return emptyResult, nil
		}

		cmd := append([]string{far.Spec.Agent}, faParams...)
		//TODO remove the below change after testing
		r.Log.Info("Execute the fence agent", "Fence Agent", far.Spec.Agent, "Node Name", node.Name, "FAR uid", far.GetUID(), "command", cmd)
		r.Executor.AsyncExecute(ctx, far.GetUID(), cmd, far.Spec.RetryCount, far.Spec.RetryInterval.Duration, far.Spec.Timeout.Duration)
		commonEvents.NormalEvent(r.Recorder, far, utils.EventReasonFenceAgentExecuted, utils.EventMessageFenceAgentExecuted)
		return emptyResult, nil
	}

	if meta.IsStatusConditionTrue(far.Status.Conditions, utils.FenceAgentActionSucceededType) &&
		!meta.IsStatusConditionTrue(far.Status.Conditions, commonConditions.SucceededType) {
		// Fence agent action succeeded
		// - try to remove workloads
		// - clean up Executor routine

		switch far.Spec.RemediationStrategy {
		case v1alpha1.ResourceDeletionRemediationStrategy, "":
			// Basically RemediationStrategy should be set to ResourceDeletion strategy as the default strategy.
			// However, it will be empty when the CS was created when ResourceDeletion strategy was the only strategy.
			// In this case, the empty strategy should be treated as if ResourceDeletion strategy selected.
			r.Log.Info("Remediation strategy is ResourceDeletion which explicitly deletes resources - manually deleting workload", "Node Name", req.Name)
			commonEvents.NormalEvent(r.Recorder, node, utils.EventReasonDeleteResources, utils.EventMessageDeleteResources)
			if err := commonResources.DeletePods(ctx, r.Client, node.Name); err != nil {
				r.Log.Error(err, "Resource deletion has failed", "CR's Name", node.Name)
				return emptyResult, err
			}
		case v1alpha1.OutOfServiceTaintRemediationStrategy:
			r.Log.Info("Remediation strategy is OutOfServiceTaint which implicitly deletes resources - adding out-of-service taint", "Node Name", req.Name)
			taintAdded, err := utils.AppendTaint(r.Client, node.Name, utils.CreateOutOfServiceTaint())
			if err != nil {
				r.Log.Error(err, "Failed to add out-of-service taint", "CR's Name", node.Name)
				return emptyResult, err
			} else if taintAdded {
				r.Log.Info("out-of-service taint was added", "Node Name", node.Name)
				commonEvents.NormalEvent(r.Recorder, node, utils.EventReasonAddOutOfServiceTaint, utils.EventMessageAddOutOfServiceTaint)
			}
		default:
			//this should never happen since we enforce valid values with kubebuilder
			err := errors.New("unsupported remediation strategy")
			r.Log.Error(err, "Encountered unsupported remediation strategy. Please check template spec", "strategy", far.Spec.RemediationStrategy)
			return emptyResult, nil
		}
		utils.UpdateConditions(utils.RemediationFinishedSuccessfully, far, r.Log)

		r.Executor.Remove(far.GetUID())
		r.Log.Info("FenceAgentsRemediation CR has completed to remediate the node", "Node Name", node.Name)
		commonEvents.NormalEvent(r.Recorder, node, utils.EventReasonNodeRemediationCompleted, utils.EventMessageNodeRemediationCompleted)
		commonEvents.RemediationFinished(r.Recorder, far)
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
	// When CR doesn't include a finalizer and the CR deletionTimestamp exists
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

// getNodeName checks for the node name in far's commonAnnotations.NodeNameAnnotation if it does not exist it assumes the node name equals to far CR's name and return it.
func getNodeName(far *v1alpha1.FenceAgentsRemediation) string {
	ann := far.GetAnnotations()
	if ann == nil {
		return far.GetName()
	}
	if nodeName, isNodeNameAnnotationExist := ann[commonAnnotations.NodeNameAnnotation]; isNodeNameAnnotationExist {
		return nodeName
	}
	return far.GetName()
}

// fetchSecret fetches a secret and returns an error on failure
func fetchSecret(ctx context.Context, c client.Client, secretKeyObj client.ObjectKey) (*corev1.Secret, error) {
	secret := &corev1.Secret{}
	if err := c.Get(ctx, secretKeyObj, secret); err != nil {
		return nil, err
	}
	return secret, nil
}

// resolveParameterValueFromSecret resolve parameter value from secret if exist, otherwise returns an error
func resolveParameterValueFromSecret(ctx context.Context, c client.Client, far *v1alpha1.FenceAgentsRemediation, paramName v1alpha1.ParameterName) (string, error) {
	var secret *corev1.Secret
	var err error
	logger := ctrl.Log.WithName("resolve-value-from-secret")
	secretName := far.Name
	// fetch secret/node name from remediation's annotation if present
	if annotatedName, exist := far.Annotations["remediation.medik8s.io/node-name"]; exist {
		secretName = annotatedName
	}
	secret, err = fetchSecret(ctx, c, client.ObjectKey{Name: secretName, Namespace: far.Namespace})
	if err != nil {
		if apiErrors.IsNotFound(err) {
			// when there is no specefic secret, then we try the default secret
			logger.Info("secret is missing - try default secret", "secret name", secretName, "default secret", DefaultSecretName, "namespace", far.Namespace, "paramter mame", string(paramName))
			secretName = DefaultSecretName
			secret, err = fetchSecret(ctx, c, client.ObjectKey{Name: secretName, Namespace: far.Namespace})
		}
		if err != nil {
			logger.Error(err, "failed to fetch secret", "secret name", secretName, "namespace", far.Namespace)
			return "", fmt.Errorf(errorFailFetchingSecret, paramName, secretName, far.Namespace, err)
		}
	}
	// Extract the secret value
	secretValue, exists := secret.Data[string(paramName)]
	if !exists {
		return "", fmt.Errorf("secret key `%s` was not found in secret `%s` at namespace `%s`", paramName, secretName, far.Namespace)
	}
	logger.Info("found a value from secret", "secret name", secretName, "paramter mame", string(paramName), "secret value", secretValue)

	return string(secretValue), nil
}

// buildFenceAgentParams collects the FAR's parameters for the node based on FAR CR, and if the CR is missing parameters
// or the CR's name don't match nodeParameter name, or it has an action which is different from reboot, then return an error
func buildFenceAgentParams(far *v1alpha1.FenceAgentsRemediation, ctx context.Context, c client.Client) ([]string, error) {
	logger := ctrl.Log.WithName("build-fa-parameters")
	if far.Spec.NodeParameters == nil || far.Spec.SharedParameters == nil {
		err := errors.New(errorMissingParams)
		logger.Error(err, "Missing parameters")
		return nil, err
	}
	var fenceAgentParams []string
	fenceAgentParamNames := make(map[v1alpha1.ParameterName]bool)

	// append shared parameters
	for paramName, paramVal := range far.Spec.SharedParameters {
		if paramName == parameterActionName && paramVal != parameterActionValue {
			// --action parameter with a differnet value from reboot is not supported
			err := errors.New("FAR doesn't support any other action than reboot")
			logger.Error(err, "can't build CR with this action attribute", "action", paramVal)
			return nil, err
		} else if _, exist := fenceAgentParamNames[paramName]; !exist {
			fenceAgentParamNames[paramName] = true
			fenceAgentParams = appendParamToSlice(fenceAgentParams, paramName, paramVal)
		}
	}

	nodeName := getNodeName(far)
	// append node parameters
	for paramName, nodeMap := range far.Spec.NodeParameters {
		if nodeVal, isFound := nodeMap[v1alpha1.NodeName(nodeName)]; isFound {
			if _, exist := fenceAgentParamNames[paramName]; !exist {
				fenceAgentParamNames[paramName] = true
				fenceAgentParams = appendParamToSlice(fenceAgentParams, paramName, nodeVal)
			}
		} else {
			err := errors.New(errorMissingNodeParams)
			logger.Error(err, "Missing matching nodeParam and CR's name")
			return nil, err
		}
	}

	// append credential parameters
	for _, paramName := range far.Spec.CredentialParameters {
		resolvedVal, err := resolveParameterValueFromSecret(ctx, c, far, paramName)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve credential parameter: %w", err)
		} else {
			if _, exist := fenceAgentParamNames[paramName]; !exist {
				fenceAgentParamNames[paramName] = true
				fenceAgentParams = appendParamToSlice(fenceAgentParams, paramName, resolvedVal)
			}
		}
	}

	// Add the reboot action with its default value - https://github.com/ClusterLabs/fence-agents/blob/main/lib/fencing.py.py#L103
	if _, exist := fenceAgentParamNames[parameterActionName]; !exist {
		logger.Info("`action` parameter is missing, so we add it with the default value of `reboot`")
		fenceAgentParams = appendParamToSlice(fenceAgentParams, parameterActionName, parameterActionValue)
	}
	return fenceAgentParams, nil
}

// appendParamToSlice appends parameters in a key-value manner, when value can be empty
func appendParamToSlice(fenceAgentParams []string, paramName v1alpha1.ParameterName, paramVal string) []string {
	stringParam := string(paramName)
	if !strings.HasPrefix(stringParam, "--") {
		stringParam = "--" + stringParam
	}
	if paramVal != "" {
		fenceAgentParams = append(fenceAgentParams, fmt.Sprintf("%s=%s", stringParam, paramVal))
	} else {
		fenceAgentParams = append(fenceAgentParams, stringParam)
	}
	return fenceAgentParams
}
