package utils

import (
	"context"

	medik8sLabels "github.com/medik8s/common/pkg/labels"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const node0 = "worker-0"

var _ = Describe("Utils-taint", func() {
	nodeKey := client.ObjectKey{Name: node0}
	controlPlaneRoleTaint := getControlPlaneRoleTaint()
	farNoExecuteTaint := CreateRemediationTaint()
	Context("Taint functioninality test", func() {
		// Check functionaility with control-plane node which already has a taint
		BeforeEach(func() {
			node := GetNode("control-plane", node0)
			Expect(k8sClient.Create(context.Background(), node)).To(Succeed())
			DeferCleanup(k8sClient.Delete, context.Background(), node)
		})
		When("Control-plane node only has 1 the control-plane-role taint", func() {
			It("should add and delete medik8s NoSchedule taint and keep other existing taints", func() {
				By("having one control-plane-role taint")
				taintedNode := &corev1.Node{}
				Expect(k8sClient.Get(context.Background(), nodeKey, taintedNode)).To(Succeed())
				// control-plane-role taint already exist by GetNode
				By("adding medik8s NoSchedule taint")
				Expect(AppendTaint(k8sClient, node0, CreateRemediationTaint())).Error().NotTo(HaveOccurred())
				Expect(k8sClient.Get(context.Background(), nodeKey, taintedNode)).To(Succeed())
				Expect(TaintExists(taintedNode.Spec.Taints, &controlPlaneRoleTaint)).To(BeTrue())
				Expect(TaintExists(taintedNode.Spec.Taints, &farNoExecuteTaint)).To(BeTrue())
				By("removing medik8s NoSchedule taint")
				// We want to see that RemoveTaint only remove the taint it receives
				Expect(RemoveTaint(k8sClient, node0, CreateRemediationTaint())).To(Succeed())
				Expect(k8sClient.Get(context.Background(), nodeKey, taintedNode)).To(Succeed())
				Expect(TaintExists(taintedNode.Spec.Taints, &controlPlaneRoleTaint)).To(BeTrue())
				Expect(TaintExists(taintedNode.Spec.Taints, &farNoExecuteTaint)).To(BeFalse())

				// there is a not-ready taint now as well, so there will be 2 taints... skip count tests
				// Expect(len(taintedNode.Spec.Taints)).To(Equal(1))
			})
		})
	})
})

// getControlPlaneRoleTaint returns a control-plane-role taint
func getControlPlaneRoleTaint() corev1.Taint {
	return corev1.Taint{
		Key:    medik8sLabels.ControlPlaneRole,
		Effect: corev1.TaintEffectNoExecute,
	}
}
