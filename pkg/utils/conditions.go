package utils

import (
	"fmt"

	"github.com/go-logr/logr"
	commonConditions "github.com/medik8s/common/pkg/conditions"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/medik8s/fence-agents-remediation/api/v1alpha1"
)

const (
	// FenceAgentActionSucceededType is the condition type used to signal whether the Fence Agent action was succeeded successfully or not
	FenceAgentActionSucceededType = "FenceAgentActionSucceeded"
	// condition messages
	RemediationFinishedNodeNotFoundConditionMessage = "FAR CR name doesn't match a node name"
	RemediationInterruptedByNHCConditionMessage     = "Node Healthcheck timeout annotation has been set"
	RemediationStartedConditionMessage              = "FAR CR was found, its name matches one of the cluster nodes, and a finalizer was set to the CR"
	FenceAgentSucceededConditionMessage             = "FAR taint was added and the fence agent command has been created and executed successfully"
	FenceAgentFailedConditionMessage                = "Fence agent command has failed"
	FenceAgentTimedOutConditionMessage              = "Time out occurred while executing the Fence agent command"
	RemediationFinishedSuccessfullyConditionMessage = "The unhealthy node was fully remediated (it was tainted, fenced using the fence agent and all the node resources have been deleted)"
)

// ConditionsChangeReason represents the reason of updating the some or all the conditions
type ConditionsChangeReason string

const (
	// RemediationFinishedNodeNotFound - CR was found but its name doesn't match any node
	RemediationFinishedNodeNotFound ConditionsChangeReason = "RemediationFinishedNodeNotFound"
	// RemediationInterruptedByNHC - Remediation was interrupted by NHC timeout annotation
	RemediationInterruptedByNHC ConditionsChangeReason = "RemediationInterruptedByNHC"
	// RemediationStarted - CR was found, its name matches a node, and a finalizer was set
	RemediationStarted ConditionsChangeReason = "RemediationStarted"
	// FenceAgentSucceeded - FAR taint was added, fence agent command has been created and executed successfully
	FenceAgentSucceeded ConditionsChangeReason = "FenceAgentSucceeded"
	// FenceAgentFailed - Fence agent command has been created but failed to execute
	FenceAgentFailed ConditionsChangeReason = "FenceAgentFailed"
	// FenceAgentTimedOut - Fence agent command has been created but timed out
	FenceAgentTimedOut ConditionsChangeReason = "FenceAgentTimedOut"
	// RemediationFinishedSuccessfully - The unhealthy node was fully remediated/fenced (it was tainted, fenced by FA and all of its resources have been deleted)
	RemediationFinishedSuccessfully ConditionsChangeReason = "RemediationFinishedSuccessfully"
)

// updateConditions updates the status conditions of a FenceAgentsRemediation object based on the provided ConditionsChangeReason.
// return an error if an unknown ConditionsChangeReason is provided
func UpdateConditions(reason ConditionsChangeReason, far *v1alpha1.FenceAgentsRemediation, log logr.Logger) {

	var (
		processingConditionStatus, fenceAgentActionSucceededConditionStatus, succeededConditionStatus metav1.ConditionStatus
		conditionMessage                                                                              string
	)
	conditionUpdateMessage := "Couldn't update FAR Status Conditions"
	unknownError := fmt.Errorf("unknown ConditionsChangeReason")
	currentConditions := &far.Status.Conditions
	conditionHasBeenChanged := false

	// RemediationFinishedNodeNotFound and RemediationInterruptedByNHC reasons can happen at any time the Reconcile runs
	// - Except these two reasons, the following reasons can only happen one after another
	// - RemediationStarted will always be the first reason (out of these three)
	// - FenceAgentSucceeded, FenceAgentFailed and FenceAgentTimedOut can only happen after RemediationStarted happened
	// - RemediationFinishedSuccessfully can only happen after FenceAgentSucceeded happened
	switch reason {
	case RemediationFinishedNodeNotFound, RemediationInterruptedByNHC, FenceAgentFailed, FenceAgentTimedOut:
		processingConditionStatus = metav1.ConditionFalse
		fenceAgentActionSucceededConditionStatus = metav1.ConditionFalse
		succeededConditionStatus = metav1.ConditionFalse
		// Different reasons share the same effect to the conditions, but they have different message
		switch reason {
		case RemediationFinishedNodeNotFound:
			conditionMessage = RemediationFinishedNodeNotFoundConditionMessage
		case RemediationInterruptedByNHC:
			conditionMessage = RemediationInterruptedByNHCConditionMessage
		case FenceAgentFailed:
			conditionMessage = FenceAgentFailedConditionMessage
		case FenceAgentTimedOut:
			conditionMessage = FenceAgentTimedOutConditionMessage
		default:
			// couldn't be reached...
			log.Error(unknownError, conditionUpdateMessage, "CR name", far.Name, "Reason", reason)
			return
		}
	case RemediationStarted:
		processingConditionStatus = metav1.ConditionTrue
		fenceAgentActionSucceededConditionStatus = metav1.ConditionUnknown
		succeededConditionStatus = metav1.ConditionUnknown
		conditionMessage = RemediationStartedConditionMessage
	case FenceAgentSucceeded:
		fenceAgentActionSucceededConditionStatus = metav1.ConditionTrue
		conditionMessage = FenceAgentSucceededConditionMessage
	case RemediationFinishedSuccessfully:
		processingConditionStatus = metav1.ConditionFalse
		succeededConditionStatus = metav1.ConditionTrue
		conditionMessage = RemediationFinishedSuccessfullyConditionMessage
	default:
		log.Error(unknownError, conditionUpdateMessage, "CR name", far.Name, "Reason", reason)
		return
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
	if fenceAgentActionSucceededConditionStatus != "" && !meta.IsStatusConditionPresentAndEqual(*currentConditions, FenceAgentActionSucceededType, fenceAgentActionSucceededConditionStatus) {
		meta.SetStatusCondition(currentConditions, metav1.Condition{
			Type:    FenceAgentActionSucceededType,
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
	log.Info("Updating Status Condition", "processingConditionStatus", processingConditionStatus, "fenceAgentActionSucceededConditionStatus", fenceAgentActionSucceededConditionStatus, "succeededConditionStatus", succeededConditionStatus, "reason", string(reason), "LastUpdateTime", far.Status.LastUpdateTime)

	return
}
