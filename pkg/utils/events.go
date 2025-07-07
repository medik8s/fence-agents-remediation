package utils

const (
	// common events reason and message
	EventReasonRemediationStarted  = "RemediationStarted"
	EventMessageRemediationStarted = "Remediation started"

	// events reasons
	EventReasonCrNodeNotFound           = "NodeNotFound"
	EventReasonCrParamInvalid           = "ParamInvalid"
	EventReasonRemediationStoppedByNHC  = "RemediationStoppedByNHC"
	EventReasonAddFinalizer             = "AddFinalizer"
	EventReasonRemoveRemediationTaint   = "RemoveRemediationTaint"
	EventReasonRemoveFinalizer          = "RemoveFinalizer"
	EventReasonAddRemediationTaint      = "AddRemediationTaint"
	EventReasonFenceAgentExecuted       = "FenceAgentExecuted"
	EventReasonFenceAgentSucceeded      = "FenceAgentSucceeded"
	EventReasonDeleteResources          = "DeleteResources"
	EventReasonAddOutOfServiceTaint     = "AddOutOfServiceTaint"
	EventReasonRemoveOutOfServiceTaint  = "RemoveOutOfServiceTaint"
	EventReasonNodeRemediationCompleted = "NodeRemediationCompleted"

	// events messages
	EventMessageCrNodeNotFound           = "CR name doesn't match a node name"
	EventMessageCrParamInvalid           = "Invalid or missing parameter value for CR"
	EventMessageRemediationStoppedByNHC  = "Remediation was stopped by the Node Healthcheck Operator"
	EventMessageAddFinalizer             = "Finalizer was added"
	EventMessageRemoveRemediationTaint   = "Remediation taint was removed"
	EventMessageRemoveFinalizer          = "Finalizer was removed"
	EventMessageAddRemediationTaint      = "Remediation taint was added"
	EventMessageFenceAgentExecuted       = "Fence agent was executed"
	EventMessageFenceAgentSucceeded      = "Fence agent was succeeded"
	EventMessageDeleteResources          = "Manually delete pods from the unhealthy node"
	EventMessageAddOutOfServiceTaint     = "The out-of-service taint was added"
	EventMessageRemoveOutOfServiceTaint  = "The out-of-service taint was removed"
	EventMessageNodeRemediationCompleted = "Unhealthy node remediation was completed"
)
