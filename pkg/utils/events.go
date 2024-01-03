package utils

const (
	// events reasons
	EventReasonCrNodeNotFound          = "NodeNotFound"
	EventReasonRemediationStoppedByNHC = "RemediationStoppedByNHC"
	EventReasonAddFinalizer            = "AddFinalizer"
	EventReasonRemoveFinalizer         = "RemoveFinalizer"
	EventReasonFenceAgentExecuted      = "FenceAgentExecuted"
	EventReasonFenceAgentSucceeded     = "FenceAgentSucceeded"

	// events messages
	EventMessageCrNodeNotFound          = "CR name doesn't match a node name"
	EventMessageRemediationStoppedByNHC = "Remediation was stopped by the Node Healthcheck Operator"
	EventMessageAddFinalizer            = "Finalizer was added"
	EventMessageRemoveFinalizer         = "Finalizer was removed"
	EventMessageFenceAgentExecuted      = "Fence agent was executed"
	EventMessageFenceAgentSucceeded     = "Fence agent was succeeded"
)
