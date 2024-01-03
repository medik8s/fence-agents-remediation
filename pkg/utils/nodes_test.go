package utils

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
)

const (
	dummyNode = "dummy-node"
	node01    = "worker-0"
)

var _ = Describe("Utils-nodes", func() {
	var node *corev1.Node
	Context("FAR CR and Node Names Validity test", func() {
		BeforeEach(func() {
			node = GetNode("", node01)
			Expect(k8sClient.Create(context.Background(), node)).To(Succeed())
			DeferCleanup(k8sClient.Delete, context.Background(), node)
		})
		When("FAR CR's name doesn't match to an existing node name", func() {
			It("should fail", func() {
				_, isMatch, _ := IsNodeNameValid(k8sClient, dummyNode)
				Expect(isMatch).To(BeFalse())
			})
		})
		When("FAR's name does match to an existing node name", func() {
			It("should succeed", func() {
				_, isMatch, _ := IsNodeNameValid(k8sClient, node01)
				Expect(isMatch).To(BeTrue())
			})
		})
	})
})
