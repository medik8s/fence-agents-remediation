package e2e

import (
	"context"
	"math/rand"
	"os"
	"time"

	commonConditions "github.com/medik8s/common/pkg/conditions"
	medik8sLabels "github.com/medik8s/common/pkg/labels"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apiErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"sigs.k8s.io/controller-runtime/pkg/client"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/medik8s/fence-agents-remediation/api/v1alpha1"
	"github.com/medik8s/fence-agents-remediation/pkg/utils"
	e2eUtils "github.com/medik8s/fence-agents-remediation/test/e2e/utils"
)

const (
	fenceAgentAWS            = "fence_aws"
	fenceAgentIPMI           = "fence_ipmilan"
	fenceAgentAction         = "reboot"
	nodeIdentifierPrefixAWS  = "--plug"
	nodeIdentifierPrefixIPMI = "--ipport"
	containerName            = "manager"
	testContainerName        = "test-container"
	testPodName              = "test-pod"

	//TODO: try to minimize timeout
	// eventually parameters
	timeoutTaint       = "2s"    // Timeout for checking the FAR taint
	timeoutReboot      = "12m0s" // fencing with fence_aws should be completed within 6 minutes
	timeoutAfterReboot = "5s"    // Timeout for verifying steps  after the node has been rebooted
	pollTaint          = "100ms"
	pollReboot         = "1s"
	pollAfterReboot    = "250ms"
	skipOOSREnvVarName = "SKIP_OOST_REMEDIATION_VERIFICATION"
)

var remediationTimes []time.Duration

var _ = Describe("FAR E2e", func() {
	var (
		fenceAgent, nodeIdentifierPrefix string
		testShareParam                   map[v1alpha1.ParameterName]string
		testNodeParam                    map[v1alpha1.ParameterName]map[v1alpha1.NodeName]string
		selectedNode                     *corev1.Node
		nodeName                         string
		pod                              *corev1.Pod
		startTime, nodeBootTimeBefore    time.Time
		err                              error
	)
	BeforeEach(func() {
		// create FAR CR spec based on OCP platformn
		clusterPlatform, err := e2eUtils.GetClusterInfo(configClient)
		Expect(err).ToNot(HaveOccurred(), "can't identify the cluster platform")
		log.Info("Begin e2e test", "Cluster name", string(clusterPlatform.Name), "PlatformType", string(clusterPlatform.Status.PlatformStatus.Type))

		switch clusterPlatform.Status.PlatformStatus.Type {
		case configv1.AWSPlatformType:
			fenceAgent = fenceAgentAWS
			nodeIdentifierPrefix = nodeIdentifierPrefixAWS
			By("running fence_aws")
		case configv1.BareMetalPlatformType:
			fenceAgent = fenceAgentIPMI
			nodeIdentifierPrefix = nodeIdentifierPrefixIPMI
			By("running fence_ipmilan")
		default:
			Skip("FAR haven't been tested on this kind of cluster (non AWS or BareMetal)")
		}

		testShareParam, err = buildSharedParameters(clusterPlatform, fenceAgentAction)
		Expect(err).ToNot(HaveOccurred(), "can't get shared information")
		testNodeParam, err = buildNodeParameters(clusterPlatform.Status.PlatformStatus.Type)
		Expect(err).ToNot(HaveOccurred(), "can't get node information")
	})

	// runFARTests is a utility function to run FAR tests.
	// It accepts a remediation strategy and a condition to determine if the tests should be skipped.
	runFARTests := func(remediationStrategy v1alpha1.RemediationStrategyType, skipCondition func() bool) {
		var availableWorkerNodes *corev1.NodeList
		BeforeEach(func() {
			if skipCondition() {
				Skip("Skip this block due to unsupported condition")
			}

			if availableWorkerNodes == nil {
				availableWorkerNodes = getAvailableWorkerNodes()
			}
			selectedNode = pickRemediatedNode(availableWorkerNodes)
			nodeName = selectedNode.Name
			printNodeDetails(selectedNode, nodeIdentifierPrefix, testNodeParam)

			// save the node's boot time prior to the fence agent call
			nodeBootTimeBefore, err = e2eUtils.GetBootTime(clientSet, nodeName, testNsName, log)
			Expect(err).ToNot(HaveOccurred(), "failed to get boot time of the node")

			// create tested pod which will be deleted by the far CR
			pod = createTestedPod(nodeName, testContainerName)
			DeferCleanup(cleanupTestedResources, pod)

			// set the node as "unhealthy" by disabling kubelet
			makeNodeUnready(selectedNode)

			startTime = time.Now()
			far := createFAR(nodeName, fenceAgent, testShareParam, testNodeParam, remediationStrategy)
			DeferCleanup(deleteFAR, far)
		})
		When("running FAR to reboot two nodes", func() {
			It("should successfully remediate the first node", func() {
				checkRemediation(nodeName, nodeBootTimeBefore, pod, remediationStrategy)
				remediationTimes = append(remediationTimes, time.Since(startTime))
			})
			It("should successfully remediate the second node", func() {
				checkRemediation(nodeName, nodeBootTimeBefore, pod, remediationStrategy)
				remediationTimes = append(remediationTimes, time.Since(startTime))
			})
		})
	}

	Context("stress cluster with ResourceDeletion remediation strategy", func() {
		runFARTests(v1alpha1.ResourceDeletionRemediationStrategy, func() bool { return false })
	})

	Context("stress cluster with OutOfServiceTaint remediation strategy", func() {
		runFARTests(v1alpha1.OutOfServiceTaintRemediationStrategy, func() bool {
			_, isExist := os.LookupEnv(skipOOSREnvVarName)
			return isExist
		})
	})
})

var _ = AfterSuite(func() {
	if len(remediationTimes) > 0 {
		averageTimeDuration := 0.0
		for _, remTime := range remediationTimes {
			averageTimeDuration += remTime.Seconds()
			log.Info("Remediation was finished", "remediation time", remTime)
		}
		averageTime := int(averageTimeDuration) / len(remediationTimes)
		log.Info("Average remediation time", "minutes", averageTime/60, "seconds", averageTime%60)
	}
})

// buildSharedParameters returns a map key-value of shared parameters based on cluster platform type if it finds the credentials, otherwise an error
func buildSharedParameters(clusterPlatform *configv1.Infrastructure, action string) (map[v1alpha1.ParameterName]string, error) {
	const (
		//AWS
		secretAWSName      = "aws-cloud-fencing-credentials-secret"
		secretAWSNamespace = "openshift-operators"
		secretKeyAWS       = "aws_access_key_id"
		secretValAWS       = "aws_secret_access_key"

		// BareMetal
		//TODO: secret BM should be based on node name - > oc get bmh -n openshift-machine-api BM_NAME -o jsonpath='{.spec.bmc.credentialsName}'
		secretBMHName      = "ostest-master-0-bmc-secret"
		secretBMHNamespace = "openshift-machine-api"
		secretKeyBM        = "username"
		secretValBM        = "password"
	)
	var testShareParam map[v1alpha1.ParameterName]string

	// oc get Infrastructure.config.openshift.io/cluster -o jsonpath='{.status.platformStatus.type}'
	clusterPlatformType := clusterPlatform.Status.PlatformStatus.Type
	if clusterPlatformType == configv1.AWSPlatformType {
		accessKey, secretKey, err := e2eUtils.GetSecretData(clientSet, secretAWSName, secretAWSNamespace, secretKeyAWS, secretValAWS)
		if err != nil {
			log.Info("Can't get AWS credentials")
			return nil, err
		}

		// oc get Infrastructure.config.openshift.io/cluster -o jsonpath='{.status.platformStatus.aws.region}'
		regionAWS := string(clusterPlatform.Status.PlatformStatus.AWS.Region)

		testShareParam = map[v1alpha1.ParameterName]string{
			"--access-key":      accessKey,
			"--secret-key":      secretKey,
			"--region":          regionAWS,
			"--action":          action,
			"--skip-race-check": "",
			// "--verbose":    "", // for verbose result
		}
	} else if clusterPlatformType == configv1.BareMetalPlatformType {
		// TODO : get ip from GetCredientals
		// oc get bmh -n openshift-machine-api ostest-master-0 -o jsonpath='{.spec.bmc.address}'
		// then parse ip
		username, password, err := e2eUtils.GetSecretData(clientSet, secretBMHName, secretBMHNamespace, secretKeyBM, secretValBM)
		if err != nil {
			log.Info("Can't get BMH credentials")
			return nil, err
		}
		testShareParam = map[v1alpha1.ParameterName]string{
			"--username": username,
			"--password": password,
			"--ip":       "192.168.111.1",
			"--action":   action,
			"--lanplus":  "",
		}
	}
	return testShareParam, nil
}

// buildNodeParameters returns a map key-value of node parameters based on cluster platform type if it finds the node info list, otherwise an error
func buildNodeParameters(clusterPlatformType configv1.PlatformType) (map[v1alpha1.ParameterName]map[v1alpha1.NodeName]string, error) {
	var (
		testNodeParam  map[v1alpha1.ParameterName]map[v1alpha1.NodeName]string
		nodeListParam  map[v1alpha1.NodeName]string
		nodeIdentifier v1alpha1.ParameterName
		err            error
	)

	if clusterPlatformType == configv1.AWSPlatformType {
		nodeListParam, err = e2eUtils.GetAWSNodeInfoList(machineClient)
		if err != nil {
			log.Info("Can't get nodes' information - AWS instance ID is missing")
			return nil, err
		}
		nodeIdentifier = v1alpha1.ParameterName(nodeIdentifierPrefixAWS)

	} else if clusterPlatformType == configv1.BareMetalPlatformType {
		nodeListParam, err = e2eUtils.GetBMHNodeInfoList(machineClient)
		if err != nil {
			log.Info("Can't get nodes' information - ports are missing")
			return nil, err
		}
		nodeIdentifier = v1alpha1.ParameterName(nodeIdentifierPrefixIPMI)
	}
	testNodeParam = map[v1alpha1.ParameterName]map[v1alpha1.NodeName]string{nodeIdentifier: nodeListParam}
	return testNodeParam, nil
}

// getAvailableNodes a list of available worker nodes in the cluster
func getAvailableWorkerNodes() *corev1.NodeList {
	availableNodes := &corev1.NodeList{}
	selector := labels.NewSelector()
	requirement, _ := labels.NewRequirement(medik8sLabels.WorkerRole, selection.Exists, []string{})
	selector = selector.Add(*requirement)
	Expect(k8sClient.List(context.Background(), availableNodes, &client.ListOptions{LabelSelector: selector})).ToNot(HaveOccurred())
	if len(availableNodes.Items) < 1 {
		Fail("No worker nodes found in the cluster")
	}
	return availableNodes
}

// pickRemediatedNode randomly returns a next remediated node from the current available nodes,
// and then the node is removed from the list of available nodes
func pickRemediatedNode(availableNodes *corev1.NodeList) *corev1.Node {
	if len(availableNodes.Items) < 1 {
		Fail("No available node found for remediation")
	}
	// Generate a random seed based on the current time
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	// Randomly select a worker node
	selectedNodeIndex := r.Intn(len(availableNodes.Items))
	selectedNode := availableNodes.Items[selectedNodeIndex]
	// Delete the selected node from the list of available nodes
	availableNodes.Items = append(availableNodes.Items[:selectedNodeIndex], availableNodes.Items[selectedNodeIndex+1:]...)
	return &selectedNode
}

// createTestedPod creates tested pod which will be deleted by the far CR
func createTestedPod(nodeName, containerName string) *corev1.Pod {
	pod := e2eUtils.GetPod(nodeName, testContainerName)
	pod.Name = testPodName
	pod.Namespace = testNsName
	pod.Spec.Tolerations = []corev1.Toleration{
		{
			Key:      v1alpha1.FARNoExecuteTaintKey,
			Operator: corev1.TolerationOpEqual,
			Effect:   corev1.TaintEffectNoExecute,
		},
	}
	Expect(k8sClient.Create(context.Background(), pod)).To(Succeed())

	return pod
}

// printNodeDetail prints the node details
func printNodeDetails(selectedNode *corev1.Node, nodeIdentifierPrefix string, testNodeParam map[v1alpha1.ParameterName]map[v1alpha1.NodeName]string) {
	nodeNameParam := v1alpha1.NodeName(selectedNode.Name)
	parameterName := v1alpha1.ParameterName(nodeIdentifierPrefix)
	testNodeID := testNodeParam[parameterName][nodeNameParam]
	log.Info("Testing Node", "Node name", selectedNode.Name, "Node ID", testNodeID)
}

// createFAR assigns the input to FenceAgentsRemediation object, creates CR, and returns the CR object
func createFAR(nodeName string, agent string, sharedParameters map[v1alpha1.ParameterName]string, nodeParameters map[v1alpha1.ParameterName]map[v1alpha1.NodeName]string, strategy v1alpha1.RemediationStrategyType) *v1alpha1.FenceAgentsRemediation {
	far := &v1alpha1.FenceAgentsRemediation{
		ObjectMeta: metav1.ObjectMeta{Name: nodeName, Namespace: operatorNsName},
		Spec: v1alpha1.FenceAgentsRemediationSpec{
			Agent:               agent,
			SharedParameters:    sharedParameters,
			NodeParameters:      nodeParameters,
			RemediationStrategy: strategy,
			RetryCount:          5,
			RetryInterval:       metav1.Duration{Duration: 20 * time.Second},
			Timeout:             metav1.Duration{Duration: 60 * time.Second},
		},
	}
	ExpectWithOffset(1, k8sClient.Create(context.Background(), far)).ToNot(HaveOccurred())
	return far
}

// deleteFAR deletes the CR with offset
func deleteFAR(far *v1alpha1.FenceAgentsRemediation) {
	EventuallyWithOffset(1, func() error {
		err := k8sClient.Delete(context.Background(), far)
		if apiErrors.IsNotFound(err) {
			return nil
		}
		return err
	}, 2*time.Minute, 10*time.Second).ShouldNot(HaveOccurred(), "failed to delete far")
}

// cleanupTestedResources deletes an old pod and old va if it was not deleted from FAR CR
func cleanupTestedResources(pod *corev1.Pod) {
	newPod := &corev1.Pod{}
	if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(pod), newPod); err == nil {
		// set GracePeriodSeconds to 0 to delete the pod immediately
		var force client.GracePeriodSeconds = 0
		Expect(k8sClient.Delete(context.Background(), newPod, force)).To(Succeed())
		log.Info("cleanup: Pod has not been deleted by remediation", "pod name", pod.Name)
	}
}

// wasTaintAdded checks whether the specified taint was added to the tested node
func wasTaintAdded(taint corev1.Taint, nodeName string) {
	var node *corev1.Node
	Eventually(func(g Gomega) bool {
		var err error
		node, err = utils.GetNodeWithName(k8sClient, nodeName)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(node).ToNot(BeNil())
		return utils.TaintExists(node.Spec.Taints, &taint)
	}, timeoutTaint, pollTaint).Should(BeTrue())
	log.Info("Taint was added", "node name", node.Name, "taint key", taint.Key, "taint effect", taint.Effect)
}

// waitForNodeHealthyCondition waits until the node's ready condition matches the given status, and it fails after timeout
func waitForNodeHealthyCondition(node *corev1.Node, condStatus corev1.ConditionStatus) {
	Eventually(func(g Gomega) corev1.ConditionStatus {
		g.Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(node), node)).To(Succeed())
		for _, cond := range node.Status.Conditions {
			if cond.Type == corev1.NodeReady {
				return cond.Status
			}
		}
		return corev1.ConditionStatus("failure")
	}, timeoutReboot, pollReboot).Should(Equal(condStatus))
}

// makeNodeUnready stops kubelet and wait for the node condition to be not ready unless the node was already unready
func makeNodeUnready(node *corev1.Node) {
	log.Info("making node unready", "node name", node.GetName())
	// check if node is unready already
	Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(node), node)).To(Succeed())
	for _, cond := range node.Status.Conditions {
		if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionUnknown {
			log.Info("node is already unready", "node name", node.GetName())
			return
		}
	}

	Expect(e2eUtils.StopKubelet(clientSet, node.Name, testNsName, log)).To(Succeed())
	waitForNodeHealthyCondition(node, corev1.ConditionUnknown)
	log.Info("node is unready", "node name", node.GetName())
}

// wasNodeRebooted waits until there is a newer boot time than before, a reboot occurred, otherwise it falls with an error
func wasNodeRebooted(nodeName string, nodeBootTimeBefore time.Time) {
	log.Info("checking if Node was rebooted", "node", nodeName, "previous boot time", nodeBootTimeBefore)
	var nodeBootTimeAfter time.Time
	Eventually(func() (time.Time, error) {
		var errBootAfter error
		nodeBootTimeAfter, errBootAfter = e2eUtils.GetBootTime(clientSet, nodeName, testNsName, log)
		if errBootAfter != nil {
			log.Error(errBootAfter, "Can't get boot time of the node")
		}
		return nodeBootTimeAfter, errBootAfter
	}, timeoutReboot, pollReboot).Should(
		BeTemporally(">", nodeBootTimeBefore), "Timeout for node reboot has passed, even though FAR CR has been created")

	log.Info("successful reboot", "node", nodeName, "offset between last boot", nodeBootTimeAfter.Sub(nodeBootTimeBefore), "new boot time", nodeBootTimeAfter)
}

// checkPodDeleted vefifies if the pod has already been deleted due to resource deletion
func checkPodDeleted(pod *corev1.Pod) {
	ConsistentlyWithOffset(1, func() bool {
		newPod := &corev1.Pod{}
		err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(pod), newPod)
		return apiErrors.IsNotFound(err)
	}, timeoutAfterReboot, pollAfterReboot).Should(BeTrue())
	log.Info("Pod has already been deleted", "pod name", pod.Name)
}

// verifyStatusCondition checks if the status condition is not set, and if it is set then it has an expected value
func verifyStatusCondition(nodeName, conditionType string, conditionStatus *metav1.ConditionStatus) {
	far := &v1alpha1.FenceAgentsRemediation{}
	farNamespacedName := client.ObjectKey{Name: nodeName, Namespace: operatorNsName}
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(context.Background(), farNamespacedName, far)).To(Succeed())
		condition := meta.FindStatusCondition(far.Status.Conditions, conditionType)
		if conditionStatus == nil {
			g.Expect(condition).To(BeNil(), "expected condition %v to not be set", conditionType)
		} else {
			g.Expect(condition).ToNot(BeNil(), "expected condition %v to be set", conditionType)
			g.Expect(condition.Status).To(Equal(*conditionStatus), "expected condition %v to have status %v", conditionType, *conditionStatus)
		}
	}, timeoutAfterReboot, pollAfterReboot).Should(Succeed())
}

// checkRemediation verify whether the node was remediated
func checkRemediation(nodeName string, nodeBootTimeBefore time.Time, pod *corev1.Pod, strategy v1alpha1.RemediationStrategyType) {

	By("Check if FAR NoExecute taint was added")
	wasTaintAdded(utils.CreateRemediationTaint(), nodeName)

	By("Getting new node's boot time")
	wasNodeRebooted(nodeName, nodeBootTimeBefore)

	if strategy == v1alpha1.OutOfServiceTaintRemediationStrategy {
		By("Check if out-of-service taint was added")
		wasTaintAdded(utils.CreateOutOfServiceTaint(), nodeName)
	}

	By("checking if old pod has been deleted")
	checkPodDeleted(pod)

	By("checking if the status conditions match a successful remediation")
	conditionStatusPointer := func(status metav1.ConditionStatus) *metav1.ConditionStatus { return &status }
	verifyStatusCondition(nodeName, commonConditions.ProcessingType, conditionStatusPointer(metav1.ConditionFalse))
	verifyStatusCondition(nodeName, utils.FenceAgentActionSucceededType, conditionStatusPointer(metav1.ConditionTrue))
	verifyStatusCondition(nodeName, commonConditions.SucceededType, conditionStatusPointer(metav1.ConditionTrue))
}
