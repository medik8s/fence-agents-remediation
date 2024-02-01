# Fence Agents Remediation (FAR)

fence-agents-remediation (*FAR*) is a Kubernetes operator that uses well-known agents to fence and remediate unhealthy nodes. The remediation includes rebooting the unhealthy node using a fence agent, and then evicting workloads from the unhealthy node. The operator is recommended when a node becomes unhealthy, and we want to completely isolate the node from a cluster, since we can’t “trust” the unhealthy node, to prevent it from accessing the shared resources like [RWO volumes](https://kubernetes.io/docs/concepts/storage/persistent-volumes/#access-modes).

FAR is one of the remediator operators by [Medik8s](https://www.medik8s.io/remediation/remediation/), such as [Self Node Remediation](https://github.com/medik8s/self-node-remediation) and [Machine Deletion Remediation](https://github.com/medik8s/machine-deletion-remediation), that were designed to run with the Node HealthCheck Operator [(NHC)](https://github.com/medik8s/node-healthcheck-operator) which detects an unhealthy node and creates remediation Custom Resource ([CR](https://kubernetes.io/docs/concepts/extend-kubernetes/api-extension/custom-resources/)). It is recommended to use FAR with NHC for an easier and smoother experience by fully automating the remediation process, but it can be used as a standalone remediator for the more experienced user. Moreover, like other Medik8s operators FAR was generated using the [operator-sdk](https://github.com/operator-framework/operator-sdk), and it supports Operator Lifecycle Manager ([OLM](https://olm.operatorframework.io/docs/)).

## About Fence Agents

FAR uses a fence agent to fence a Kubernetes node. Generally, fencing is the process of taking unresponsive/unhealthy computers into a safe state and isolating the computer. Fence agent is a software code that uses a management interface to perform fencing, mostly power-based fencing which enables power-cycling, resetting, or turning off the computer.

FAR uses some of the fence agents from the [upstream repository](https://github.com/ClusterLabs/fence-agents) by the *ClusterLabs* group. For example, `fence_ipmilan` for Intelligent Platform Management Interface ([IPMI](https://en.wikipedia.org/wiki/Intelligent_Platform_Management_Interface)) environments or `fence_aws` for Amazon Web Services ([AWS](https://aws.amazon.com)) platform. These upstream fence agents are Python scripts that are used to isolate a corrupted node from the rest of the cluster in a power-based fencing method. When a node is switched off, it cannot corrupt any data on shared storage. The fence agents use command-line arguments rather than configuration files, and to understand better the parameters you can view the fence agent's metadata (e.g., `fence_ipmilan -o metadata`).

## Advantages

* Robustness - FAR has direct feedback from the traditional Application Programming Interface (API) call (e.g., IPMI) about the result of the fence action without using the Kubernetes API.
* Speed - FAR is rapid since it can reboot a node and receive an acknowledgment from the API call while other remediators might need to wait a safe time till they can expect the node to be rebooted.
* Availability - FAR has high availiabilty by running with two replicas of its pod, and when the leader of these two pods is evicted, then the other one takes control and reduces FAR downtime.
* Diversity - FAR includes several fence agents from a large known set of upstream fencing agents for bare metal servers, virtual machines, cloud platforms, etc.
* Adjustability - FAR allows to set up different parameters for running the API call that remediates the node.

## How does FAR work?

The operator watches for new or deleted CRs called `FenceAgentsRemediation` (or `far`) which trigger remediation for the node, based on the CR's name. When the CR name doesn't match a node in the cluster, then the CR won't trigger any remediation by FAR. Remediation includes adding a taint on the node, rebooting the node by fence agent, and at last deleting the remaining workloads.

FAR remediates a node by simply rebooting the unhealthy node, and moving any remaining workloads from the node to other nodes, so they can be procced in other nodes and be isolated from the unhealthy node. The reboot is done by executing a fence agent for the unhealthy node while evicting the workloads from this node is achieved by tainting the node and deleting the workloads. FAR unique taint, `medik8s.io/fence-agents-remediation`, has a [NoExecute effect](https://kubernetes.io/docs/concepts/scheduling-eviction/taint-and-toleration/#taint-based-evictions), so  any pods that don't tolerate this taint are evicted immediately, and they won't be scheduled again after the node has been rebooted as long as the taint remains (the taint is removed on FenceAgentsRemediation CR deleteion). Deleting the workloads is done to speed up kubenrentes rescheduling of the remaining pods (most likely [stateful pods](https://kubernetes.io/docs/concepts/workloads/controllers/statefulset/#using-statefulsets)), that are not running anymore.

FAR includes the `FenceAgentsRemediationTemplate` (or `fartemplate`) for creating FenceAgentsRemediation CRs with NHC. The template is important as it includes the fence agent name and its parameters for accessing the management interface. A user must create a far template to be used for remediating a node since FAR doesn't automatically create this kind of CR. When FAR is used with NHC, then NHC will use fartemplate CR to create (and eventually delete) FenceAgentsRemediation CR with the name of the unhealthy node.

### Operator Workflow

#### Prerequisites

* FAR and NHC are installed on the cluster.
* One of the nodes fails (it has become unhealthy), and NHC detects this node as unhealthy and decides to create a remediation CR, FenceAgentsRemediation CR, based on the external remediator template (e.g., fartemplate).

#### Workflow

1. FAR adds NoExecute taint to the failed node
=> Ensure that any workloads are not executed after rebooting the failed node, and any stateless pods (that can’t tolerate FAR NoExecute taint) will be evicted immediately
2. FAR reboots the failed node via the Fence Agent
=> After rebooting, there are no workloads in the failed node
3. FAR forcefully deletes the pods in the failed node
=> The scheduler understands that it can schedule the failed pods on a different node
4. After the failed node becomes healthy, NHC deletes FenceAgentsRemediation CR, the NoExecute taint in Step 2 is removed, and the node becomes schedulable again

## Installation

There are two ways to install the operator:

* Deploy the latest version, which was built from the `main` branch, to a running Kubernetes/OpenShift cluster.
<!-- TODO: - Deploy the last release version from OperatorHub to a running Kubernetes cluster. -->
* Build and deploy from sources to a running or to be created Kubernetes/OpenShift cluster.

### Deploy the latest version

After every PR is merged to the `main` branch, then the images are built and pushed to [`quay.io`](quay.io/medik8s/fence-agents-remediation-operator-bundle) (due to the [*post-submit* job](https://github.com/medik8s/fence-agents-remediation/blob/main/.github/workflows/post-submit.yaml) ).
For deployment of FAR using these images you need:

* Install `operator-sdk` binary from their [official website](https://sdk.operatorframework.io/docs/installation/#install-from-github-release).

* A running OpenShift cluster, or a Kubernetes cluster with Operator Lifecycle Manager ([OLM](https://olm.operatorframework.io/docs/)) installed (to install it run `operator-sdk olm install`).

* A valid `$KUBECONFIG` configured to access your cluster.
<!-- TODO: ATM it can't be installed on the default namespace -->
Then, run `operator-sdk run bundle quay.io/medik8s/fence-agents-remediation-operator-bundle:latest` to deploy the FAR's latest version on the current namespace.

### Build and deploy from sources

* Clone FAR repository.

* Follow OLM's [instructions](https://sdk.operatorframework.io/docs/building-operators/golang/tutorial/#configure-the-operators-image-registry) on how to configure the operator's image registry (build and push the operator container).
* Run FAR using one the [suggested options from OLM](https://sdk.operatorframework.io/docs/building-operators/golang/tutorial/#run-the-operator) to run it locally, in the cluster, and in the cluster using bundle container (similar to the [above installation](#deploy-the-latest-version)).

## Usage

FAR is recommended for using with NHC to create a complete solution for unhealthy nodes, since NHC detects unhealthy nodes and creates an external remediation CR, e.g., FAR's CR, for unhealthy nodes.
This automated way is preferable as it gives the responsibility on FAR CRs (creation and deletion) to NHC, even though FAR can also act as standalone remediator, but it with expense from the administrator to create and delete CRs.

Either way a user must be familiar with fence agent to be used - Knowing its parameters and any other requirements on the cluster (e.g., fence_ipmilan needs machines that support IPMI).

### FAR with NHC

* Install FAR using one of the above options ([Installation](#installation)).

* Load the yaml manifest of the FAR template (see below).

* Modify NHC CR to use FAR as its remediator -
This is basically a specific use case of an [external remediation of NHC CR](https://github.com/medik8s/node-healthcheck-operator#external-remediation-resources).
In order to set it up, please make sure that Node Health Check is running, FAR controller exists and then creates the necessary CRs (*FenceAgentsRemediationTemplate* and then *NodeHealthCheck*).

#### Example CRs

The FAR template, `FenceAgentsRemediationTemplate`, CR is created by the administrator and is used as a template by NHC for creating the NHC CR that represent a request for a Node to be recovered.
For better understanding please see the below example of FAR template object (see it also as the [sample FAR template](https://github.com/medik8s/fence-agents-remediation/blob/main/config/samples/fence-agents-remediation_v1alpha1_fenceagentsremediationtemplate.yaml)):

```yaml
apiVersion: fence-agents-remediation.medik8s.io/v1alpha1
kind: FenceAgentsRemediationTemplate
metadata:
  name: fenceagentsremediationtemplate-default
spec:
  template: {}
```

> *Note*:  FenceAgentsRemediationTemplate CR must be created in the same namespace that FAR operator has been installed.

Configuring NodeHealthCheck to use the example `fenceagentsremediationtemplate-default` template above.

```yaml
apiVersion: remediation.medik8s.io/v1alpha1
kind: NodeHealthCheck
metadata:
  name: nodehealthcheck-sample
spec:
  remediationTemplate:
    apiVersion: fence-agents-remediation.medik8s.io/v1alpha1
    kind: FenceAgentsRemediationTemplate
    name: fenceagentsremediationtemplate-default
    namespace: default
```

NHC creates FAR CR using FAR Template after it detects an unhealthy node (according to NHC unhealthy conditions).
FAR CRs are deleted by NHC after it sees the Node is healthy again.

### Standalone FAR

* Install FAR using one of the above options ([Installation](#installation)).

* Create FAR CR using the name of the node to be remediated, and the fence-agent parameters.

#### Example CR

The FAR CR (CustomResource), created by the admin, is used to trigger the fence-agent on a specific node.

The CR includes the following parameters:

* `agent` - fence-agent name
* `sharedparameters` - cluster wide parameters for executing the fence agent
* `nodeparameters` - node specific parameters for executing the fence agent
* `retrycount` - number of times to retry the fence-agent in case of failure. The default is 5.
* `retryinterval` - interval between retries in seconds. The default is "5s".
* `timeout` - timeout for the fence-agent in seconds. The default is "60s".

For better understanding please see the below example of FAR CR for node `worker-1` (see it also as the [sample FAR](https://github.com/medik8s/fence-agents-remediation/blob/main/config/samples/fence-agents-remediation_v1alpha1_fenceagentsremediation.yaml)):

```yaml
apiVersion: fence-agents-remediation.medik8s.io/v1alpha1
kind: FenceAgentsRemediation
metadata:
  name: worker-1
spec:
  agent: fence_ipmilan
  retrycount: 5
  retryinterval: "5s"
  timeout: "60s"
  sharedparameters:
    --username: "admin"
    --password: "password"
    --lanplus: ""
    --action: "reboot"
    --ip: "192.168.111.1"
  nodeparameters:
    --ipport:
      master-0: "6230"
      master-1: "6231"
      master-2: "6232"
      worker-0: "6233"
      worker-1: "6234"
      worker-2: "6235"
```

## Tests

### Run code checks and unit tests

`make test`

### Run e2e tests

1. Deploy the operator as explained above
2. Run `make test-e2e`

## Help

Feel free to join our Google group to get more info - [Medik8s Google Group](https://groups.google.com/g/medik8s).
