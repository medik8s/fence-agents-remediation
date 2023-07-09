package e2e

import (
	"context"
	"fmt"
	"time"

	medik8sLabels "github.com/medik8s/common/pkg/labels"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apiErrors "k8s.io/apimachinery/pkg/api/errors"
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
	succeesRebootMessage     = "\"Success: Rebooted"
	containerName            = "manager"

	//TODO: try to minimize timeout
	// eventually parameters
	timeoutLogs   = 3 * time.Minute
	timeoutReboot = 6 * time.Minute // fencing with fence_aws should be completed within 6 minutes
	pollInterval  = 10 * time.Second
)

var _ = Describe("FAR E2e", func() {
	var (
		fenceAgent, nodeIdentifierPrefix string
		testShareParam                   map[v1alpha1.ParameterName]string
		testNodeParam                    map[v1alpha1.ParameterName]map[v1alpha1.NodeName]string
		nodeIndex                        int
		secondRun                        bool
	)
	BeforeEach(func() {
		// create FAR CR spec based on OCP platformn
		clusterPlatform, err := e2eUtils.GetClusterInfo(configClient)
		if err != nil {
			Fail("can't identify the cluster platform")
		}
		fmt.Printf("\ncluster name: %s and PlatformType: %s \n", string(clusterPlatform.Name), string(clusterPlatform.Status.PlatformStatus.Type))

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
		if err != nil {
			Fail("can't get shared information")
		}
		testNodeParam, err = buildNodeParameters(clusterPlatform.Status.PlatformStatus.Type)
		if err != nil {
			Fail("can't get node information")
		}
		// run FA on the first worker node
		nodeIndex = 0
	})

	Context("stress cluster", func() {
		var (
			nodeName, errString string
			nodeBootTimeBefore  time.Time
			err                 error
		)
		BeforeEach(func() {
			nodes := &corev1.NodeList{}
			selector := labels.NewSelector()
			requirement, _ := labels.NewRequirement(medik8sLabels.WorkerRole, selection.Exists, []string{})
			selector = selector.Add(*requirement)
			Expect(k8sClient.List(context.Background(), nodes, &client.ListOptions{LabelSelector: selector})).ToNot(HaveOccurred())
			if len(nodes.Items) < 1 {
				Fail("there are no worker nodes in the cluster")
			}
			if secondRun {
				nodeIndex++
			}
			nodeName, errString = getNodeName(nodeIndex)
			if errString != "" {
				if nodeIndex <= 0 {
					Fail(errString)
				}
				Skip(errString)
			}
			nodeNameParam := v1alpha1.NodeName(nodeName)
			parameterName := v1alpha1.ParameterName(nodeIdentifierPrefix)
			testNodeID := testNodeParam[parameterName][nodeNameParam]
			log.Info("Testing Node", "Node name", nodeName, "Node ID", testNodeID)

			// save the node's boot time prior to the fence agent call
			nodeBootTimeBefore, err = e2eUtils.GetBootTime(clientSet, nodeName, testNsName, log)
			Expect(err).ToNot(HaveOccurred(), "failed to get boot time of the node")
			far := createFAR(nodeName, fenceAgent, testShareParam, testNodeParam)
			DeferCleanup(deleteFAR, far)
		})
		When("running FAR to reboot two nodes", func() {
			It("should successfully remediate the first node", func() {
				remediateNode(nodeName, succeesRebootMessage, nodeBootTimeBefore)
				// next run create CR for the next worker node
				secondRun = true
			})
			It("should successfully remediate the second node", func() {
				remediateNode(nodeName, succeesRebootMessage, nodeBootTimeBefore)

			})
		})
	})
})

// getNodeName returns the node's name based on valid index, otherwise it returns an error
func getNodeName(index int) (string, string) {
	nodes := &corev1.NodeList{}
	selector := labels.NewSelector()
	requirement, _ := labels.NewRequirement(utils.WorkerLabelName, selection.Exists, []string{})
	selector = selector.Add(*requirement)
	Expect(k8sClient.List(context.Background(), nodes, &client.ListOptions{LabelSelector: selector})).ToNot(HaveOccurred())
	if index < 0 {
		return "", "nodeIndex is invalid - smaller than zero"
	}
	if index >= len(nodes.Items) {
		return "", fmt.Sprintf("nodeIndex is invalid - there are not enough available worker nodes for nodeIndex %d", index)
	}
	return nodes.Items[index].Name, ""
}

// createFAR assigns the input to FenceAgentsRemediation object, creates CR, and returns the CR object
func createFAR(nodeName string, agent string, sharedParameters map[v1alpha1.ParameterName]string, nodeParameters map[v1alpha1.ParameterName]map[v1alpha1.NodeName]string) *v1alpha1.FenceAgentsRemediation {
	far := &v1alpha1.FenceAgentsRemediation{
		ObjectMeta: metav1.ObjectMeta{Name: nodeName, Namespace: operatorNsName},
		Spec: v1alpha1.FenceAgentsRemediationSpec{
			Agent:            agent,
			SharedParameters: sharedParameters,
			NodeParameters:   nodeParameters,
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
			fmt.Printf("can't get AWS credentials\n")
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
			fmt.Printf("can't get BMH credentials\n")
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
			fmt.Printf("can't get nodes' information - AWS instance ID\n")
			return nil, err
		}
		nodeIdentifier = v1alpha1.ParameterName(nodeIdentifierPrefixAWS)

	} else if clusterPlatformType == configv1.BareMetalPlatformType {
		nodeListParam, err = e2eUtils.GetBMHNodeInfoList(machineClient)
		if err != nil {
			fmt.Printf("can't get nodes' information - ports\n")
			return nil, err
		}
		nodeIdentifier = v1alpha1.ParameterName(nodeIdentifierPrefixIPMI)
	}
	testNodeParam = map[v1alpha1.ParameterName]map[v1alpha1.NodeName]string{nodeIdentifier: nodeListParam}
	return testNodeParam, nil
}

// checkFarLogs gets the FAR pod and checks whether it's logs have logString
func checkFarLogs(logString string) {
	EventuallyWithOffset(1, func() string {
		pod, err := utils.GetFenceAgentsRemediationPod(k8sClient)
		if err != nil {
			log.Error(err, "failed to get FAR pod. Might try again")
			return ""
		}
		logs, err := e2eUtils.GetLogs(clientSet, pod, containerName)
		if err != nil {
			if apiErrors.IsNotFound(err) {
				// If FAR pod was running in nodeObj, then after reboot it was recreated in another node, and with a new name.
				// Thus the "old" pod's name prior to this eventually won't link to a running pod, since it was already evicted by the reboot
				log.Error(err, "failed to get logs. FAR pod might have been recreated due to rebooting the node it was resided. Might try again", "pod", pod.Name)
				return ""
			}
			log.Error(err, "failed to get logs. Might try again", "pod", pod.Name)
			return ""
		}
		return logs
	}, timeoutLogs, pollInterval).Should(ContainSubstring(logString))
}

// wasNodeRebooted waits until there is a newer boot time than before, a reboot occurred, otherwise it falls with an error
func wasNodeRebooted(nodeName string, nodeBootTimeBefore time.Time) {
	log.Info("boot time", "node", nodeName, "old", nodeBootTimeBefore)
	var nodeBootTimeAfter time.Time
	Eventually(func() (time.Time, error) {
		var errBootAfter error
		nodeBootTimeAfter, errBootAfter = e2eUtils.GetBootTime(clientSet, nodeName, testNsName, log)
		if errBootAfter != nil {
			log.Error(errBootAfter, "Can't get boot time of the node")
		}
		return nodeBootTimeAfter, errBootAfter
	}, timeoutReboot, pollInterval).Should(
		BeTemporally(">", nodeBootTimeBefore), "Timeout for node reboot has passed, even though FAR CR has been created")

	log.Info("successful reboot", "node", nodeName, "offset between last boot", nodeBootTimeAfter.Sub(nodeBootTimeBefore), "new boot time", nodeBootTimeAfter)
}

// remediateNode run three functions to verify whether the node was remediated
func remediateNode(nodeName, logString string, nodeBootTimeBefore time.Time) {
	By("Executing the FA command and receive success response")
	// TODO: When reboot is running only once and it is running on FAR node, then FAR pod will
	// be recreated on a new node and since the FA command won't be exuected again, then the log
	// won't include any success message
	checkFarLogs(succeesRebootMessage)

	By("Getting new node's boot time")
	wasNodeRebooted(nodeName, nodeBootTimeBefore)
}
