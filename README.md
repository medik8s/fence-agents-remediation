# Fence Agents Remediation (FAR)
The fence-agents-remediation (*FAR*) is an operator generated from the operator-sdk. This operator is desgined to run existing set of [upstream fencing agents](https://github.com/ClusterLabs/fence-agents) for environments with a traditional API end-point (eg. [IPMI](https://en.wikipedia.org/wiki/Intelligent_Platform_Management_Interface)) for power cycling cluster nodes.

The operator watches for new or deleted custom resources (CRs) called `FenceAgentsRemediation` which trigger a fence-agent to a node, based on the fence-agent-remediation-template CR, `FenceAgentsRemediationTemplate`, and the FenceAgentsRemediation CR's name.

FAR has been tested with one fence-agent [fence_ipmilan](https://www.mankier.com/8/fence_ipmilan) - I/O Fencing agent which can be used with machines controlled by IPMI, and using [ipmitool](<http://ipmitool.sf.net/>).

# Help
Feel free to join our Google group to get more info - https://groups.google.com/g/medik8s