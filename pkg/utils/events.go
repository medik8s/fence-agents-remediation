package utils

const (
	// common events reason and message
	EventReasonRemediationStarted  = "RemediationStarted"
	EventMessageRemediationStarted = "Remediation started"

	// events reasons
	EventReasonCrNodeNotFound           = "NodeNotFound"
	EventReasonRemediationStoppedByNHC  = "RemediationStoppedByNHC"
	EventReasonAddFinalizer             = "AddFinalizer"
	EventReasonRemoveRemediationTaint   = "RemoveRemediationTaint"
	EventReasonRemoveFinalizer          = "RemoveFinalizer"
	EventReasonAddRemediationTaint      = "AddRemediationTaint"
	EventReasonFenceAgentExecuted       = "FenceAgentExecuted"
	EventReasonFenceAgentSucceeded      = "FenceAgentSucceeded"
	EventReasonDeleteResources          = "DeleteResources"
	EventReasonNodeRemediationCompleted = "NodeRemediationCompleted"

	// events messages
	EventMessageCrNodeNotFound           = "CR name doesn't match a node name"
	EventMessageRemediationStoppedByNHC  = "Remediation was stopped by the Node Healthcheck Operator"
	EventMessageAddFinalizer             = "Finalizer was added"
	EventMessageRemoveRemediationTaint   = "Remediation taint was removed"
	EventMessageRemoveFinalizer          = "Finalizer was removed"
	EventMessageAddRemediationTaint      = "Remediation taint was added"
	EventMessageFenceAgentExecuted       = "Fence agent was executed"
	EventMessageFenceAgentSucceeded      = "Fence agent was succeeded"
	EventMessageDeleteResources          = "Manually delete pods from the unhealthy node"
	EventMessageNodeRemediationCompleted = "Unhealthy node remediation was completed"
)
