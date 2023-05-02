package e2e

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/medik8s/fence-agents-remediation/api/v1alpha1"
	farUtils "github.com/medik8s/fence-agents-remediation/test/e2e/utils"
	corev1 "k8s.io/api/core/v1"
	apiErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	fenceAgentDummyName = "echo"
	testNamespace       = "openshift-operators"
	fenceAgentAWS       = "fence_aws"
)

var _ = Describe("FAR E2e", func() {
	var (
		far        *v1alpha1.FenceAgentsRemediation
		fenceAgent string
	)

	// command -> oc get Infrastructure.config.openshift.io/cluster -o jsonpath='{.spec.platformSpec.yype}'
	clusterPlatform, err := farUtils.GetClusterInfo(&configClient)
	clusterPlatformType := string(clusterPlatform.Spec.PlatformSpec.Type)
	if err != nil {
		Fail("can't identify the cluster platform")
	}
	log.Info("Clustetr Platform", "type", clusterPlatformType)

	Context("fence agent - dummy", func() {
		testNodeName := "dummy-node"

		BeforeEach(func() {
			testShareParam := map[v1alpha1.ParameterName]string{}
			testNodeParam := map[v1alpha1.ParameterName]map[v1alpha1.NodeName]string{}
			far = createFAR(testNodeName, fenceAgentDummyName, testShareParam, testNodeParam)
		})

		AfterEach(func() {
			deleteFAR(far)
		})

		It("should check whether the CR has been created", func() {
			testFarCR := &v1alpha1.FenceAgentsRemediation{}
			Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(far), testFarCR)).To(Succeed(), "failed to get FAR CR")
		})
	})

	Context("fence agent - non-Dummy", func() {
		//testShareParam,testNodeParam := buildParameters(clusterPlatform, "status")

		if clusterPlatformType == "AWS" {
			fenceAgent = fenceAgentAWS
			By("running fence_aws")
			// } else if clusterPlatformType == "BareMetal"{
			// 	fenceAgent = fenceAgentIPMI
			// 	By("running fence_ipmilan")
		} else {
			Skip("FAR haven't been tested on this kind of cluster (non AWS or BareMetal)")
		}

		accessKey, secretKey, err := farUtils.GetAWSCredientals(clientSet)
		if err != nil {
			Fail("can't get AWS credentials")
		}

		// command -> oc get Infrastructure.config.openshift.io/cluster  -o jsonpath='{.status.platformStatus.aws.region}'
		regionAWS := string(clusterPlatform.Status.PlatformStatus.AWS.Region)
		actionAWS := "status"

		testShareParam := map[v1alpha1.ParameterName]string{
			"--access-key": accessKey,
			"--secret-key": secretKey,
			"--region":     regionAWS,
			"--action":     actionAWS,
			"--verbose":    "",
		}

		nodeListParam, err := farUtils.GetNodeInfoList(machineClient)
		if err != nil {
			Fail("can't get nodes' information- AWS instance ID")
		}
		nodeIdentifier := v1alpha1.ParameterName("--plug")
		testNodeParam := map[v1alpha1.ParameterName]map[v1alpha1.NodeName]string{nodeIdentifier: nodeListParam}

		var testNodeName string
		nodes := &corev1.NodeList{}

		BeforeEach(func() {
			Expect(k8sClient.List(context.Background(), nodes, &client.ListOptions{})).ToNot(HaveOccurred())
			if len(nodes.Items) <= 1 {
				Fail("there is one or less available nodes in the cluster")
			}
			//TODO: Randomize the node selection
			// run FA on the first node - a master node
			nodeObj := nodes.Items[0]
			testNodeName = nodeObj.Name
			log.Info("Testing Node", "Node name", testNodeName)

			far = createFAR(testNodeName, fenceAgent, testShareParam, testNodeParam)
		})

		AfterEach(func() {
			deleteFAR(far)
		})

		When("running FAR to reboot node ", func() {
			It("should execute the fence agent cli command", func() {
				By("checking the CR has been created")
				testFarCR := &v1alpha1.FenceAgentsRemediation{}
				Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(far), testFarCR)).To(Succeed(), "failed to get FAR CR")

			})
		})
	})
})

// createFAR assigns the input to FenceAgentsRemediation object, creates CR, and returns the CR object
func createFAR(nodeName string, agent string, sharedParameters map[v1alpha1.ParameterName]string, nodeParameters map[v1alpha1.ParameterName]map[v1alpha1.NodeName]string) *v1alpha1.FenceAgentsRemediation {
	far := &v1alpha1.FenceAgentsRemediation{
		ObjectMeta: metav1.ObjectMeta{Name: nodeName, Namespace: testNamespace},
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
