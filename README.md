# Fence Agents Remediation (FAR)

The fence-agents-remediation (*FAR*) is a Kubrenetes operator generated using the [operator-sdk](https://github.com/operator-framework/operator-sdk), and it is part of [Medik8s](https://github.com/medik8s) operators. This operator is desgined to run an existing set of [upstream fencing agents](https://github.com/ClusterLabs/fence-agents) for environments with a traditional API end-point (e.g., [IPMI](https://en.wikipedia.org/wiki/Intelligent_Platform_Management_Interface)) for power cycling cluster nodes.

The operator watches for new or deleted custom resources (CRs) called `FenceAgentsRemediation` (or `far`) which trigger a fence-agent to remediate a node, based on the CR's name.
FAR operator was designed to run with the Node HealthCheck Operator [(NHC)](https://github.com/medik8s/node-healthcheck-operator) as extrenal remediatior for easier and smoother experience, but it can be used as a standalone remeidatior for the more advanced user.
FAR joins Medik8s as another remediator alternative for NHC, apart from [Self Node Remediation](https://github.com/medik8s/self-node-remediation) and [Machine Deletion Remediation](https://github.com/medik8s/machine-deletion-remediation) which are also from the [Medik8s](https://www.medik8s.io/) group.

FAR operator includes plenty of well known [fence-agents](https://github.com/medik8s/fence-agents-remediation/blob/main/Dockerfile#L31) to choose from (see [here](https://github.com/ClusterLabs/fence-agents/tree/main/agents) for the full list), thanks to the upstream [fence-agents repo](https://github.com/ClusterLabs/fence-agents) from *ClusterLabs*.
Currently FAR has been tested only with one fence-agent [*fence_ipmilan*](https://www.mankier.com/8/fence_ipmilan) - I/O Fencing agent which can be used with machines controlled by IPMI, and using [ipmitool](<http://ipmitool.sf.net/>).

## Tests

### Run code checks and unit tests

`make test`

### Run e2e tests

1. Deploy the operator as explained above
2. Run `make test-e2e`

## Help

Feel free to join our Google group to get more info - [Medik8s Google Group](https://groups.google.com/g/medik8s).
