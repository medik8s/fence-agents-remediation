# Fence Agents Remediation (FAR)

The fence-agents-remediation (*FAR*) is a Kubrenetes operator generated using the [operator-sdk](https://github.com/operator-framework/operator-sdk), and it is part of [Medik8s](https://github.com/medik8s) operators. This operator is desgined to run an existing set of [upstream fencing agents](https://github.com/ClusterLabs/fence-agents) for environments with a traditional API end-point (e.g., [IPMI](https://en.wikipedia.org/wiki/Intelligent_Platform_Management_Interface)) for power cycling cluster nodes.

The operator watches for new or deleted custom resources (CRs) called `FenceAgentsRemediation` (or `far`) which trigger a fence-agent to remediate a node, based on the CR's name.
FAR operator was designed to run with the Node HealthCheck Operator [(NHC)](https://github.com/medik8s/node-healthcheck-operator) as extrenal remediatior for easier and smoother experience, but it can be used as a standalone remeidatior for the more advanced user.
FAR joins Medik8s as another remediator alternative for NHC, apart from [Self Node Remediation](https://github.com/medik8s/self-node-remediation) and [Machine Deletion Remediation](https://github.com/medik8s/machine-deletion-remediation) which are also from the [Medik8s](https://www.medik8s.io/) group.

FAR operator includes plenty of well known [fence-agents](https://github.com/medik8s/fence-agents-remediation/blob/main/Dockerfile#L31) to choose from (see [here](https://github.com/ClusterLabs/fence-agents/tree/main/agents) for the full list), thanks to the upstream [fence-agents repo](https://github.com/ClusterLabs/fence-agents) from *ClusterLabs*.
Currently FAR has been tested only with one fence-agent [*fence_ipmilan*](https://www.mankier.com/8/fence_ipmilan) - I/O Fencing agent which can be used with machines controlled by IPMI, and using [ipmitool](<http://ipmitool.sf.net/>).

## Installation

There are two ways to install the operator:

* Deploy the latest version, which was built from the `main` branch, to a running Kubernetes/OpenShift cluster.
<!-- TODO: - Deploy the last release version from OperatorHub to a running Kubernetes cluster. -->
* Build and deploy from sources to a running or to be created Kubernetes/OpenShift cluster.

### Deploy the latest version

After every PR is merged to the `main` branch, then the images are built and pushed to [`quay.io`](quay.io/medik8s/fence-agents-remediation-operator-bundle) (due to the [*post-submit* job](https://github.com/medik8s/fence-agents-remediation/blob/main/.github/workflows/post-submit.yaml) ).
For deployment of FAR using these images you need:

* Install `operator-sdk` binary from their [offical website](https://sdk.operatorframework.io/docs/installation/#install-from-github-release).

* A running OpenShift cluster, or a Kubernetes cluster with Operator Lifecycle Manager ([OLM](https://olm.operatorframework.io/docs/)) installed (to install it run `operator-sdk olm install`).

* A valid `$KUBECONFIG` configured to access your cluster.
<!-- TODO: ATM it can't be installed on the default namespace -->
Then, run `operator-sdk run bundle quay.io/medik8s/fence-agents-remediation-operator-bundle:latest` to deploy the FAR's latest version on the current namespace.

### Build and deploy from sources

* Clone FAR repoistory.

* Follow OLM's [instructions](https://sdk.operatorframework.io/docs/building-operators/golang/tutorial/#configure-the-operators-image-registry) on how to configure the operator's image reistry (build and push the operator container).
* Run FAR using one the [suggested options from OLM](https://sdk.operatorframework.io/docs/building-operators/golang/tutorial/#run-the-operator) to run it locally, in the cluster, and in the cluster using bundle container (similar to the [above installation](#deploy-the-latest-version)).

## Tests

### Run code checks and unit tests

`make test`

### Run e2e tests

1. Deploy the operator as explained above
2. Run `make test-e2e`

## Help

Feel free to join our Google group to get more info - [Medik8s Google Group](https://groups.google.com/g/medik8s).
