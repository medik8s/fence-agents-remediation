package e2e

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	apiErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/medik8s/fence-agents-remediation/api/v1alpha1"
)

const (
	fenceAgentDummyName = "echo"
	testNamespace       = "openshift-operators"
)

var _ = Describe("FAR E2e", func() {
	Context("fence agent - dummy", func() {
		var far *v1alpha1.FenceAgentsRemediation
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
})

// createFAR assigns the input to FenceAgentsRemediation object, creates CR with offset, and returns the CR object
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
