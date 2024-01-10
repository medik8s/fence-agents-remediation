package v1alpha1

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("FenceAgentsRemediationTemplate Validation", func() {

	Context("creating FenceAgentsRemediationTemplate", func() {

		When("agent name match format and binary", func() {
			It("should be accepted", func() {
				farTemplate := getTestFARTemplate(validAgentName)
				_, err := farTemplate.ValidateCreate()
				Expect(err).ToNot(HaveOccurred())
			})
		})

		When("agent name was not found ", func() {
			It("should be rejected", func() {
				farTemplate := getTestFARTemplate(invalidAgentName)
				_, err := farTemplate.ValidateCreate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("unsupported fence agent: %s", invalidAgentName))
			})
		})
	})

	Context("updating FenceAgentsRemediationTemplate", func() {
		var oldFARTemplate *FenceAgentsRemediationTemplate
		When("agent name match format and binary", func() {
			BeforeEach(func() {
				oldFARTemplate = getTestFARTemplate(invalidAgentName)
			})
			It("should be accepted", func() {
				farTemplate := getTestFARTemplate(validAgentName)
				_, err := farTemplate.ValidateUpdate(oldFARTemplate)
				Expect(err).ToNot(HaveOccurred())
			})
		})

		When("agent name was not found ", func() {
			BeforeEach(func() {
				oldFARTemplate = getTestFARTemplate(invalidAgentName)
			})
			It("should be rejected", func() {
				farTemplate := getTestFARTemplate(invalidAgentName)
				_, err := farTemplate.ValidateUpdate(oldFARTemplate)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("unsupported fence agent: %s", invalidAgentName))
			})
		})
	})
})

func getTestFARTemplate(agentName string) *FenceAgentsRemediationTemplate {
	return &FenceAgentsRemediationTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-" + agentName + "-template",
		},
		Spec: FenceAgentsRemediationTemplateSpec{
			Template: FenceAgentsRemediationTemplateResource{
				Spec: FenceAgentsRemediationSpec{
					Agent: agentName,
				},
			},
		},
	}
}
