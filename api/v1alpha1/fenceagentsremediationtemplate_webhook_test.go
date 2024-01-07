package v1alpha1

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/medik8s/fence-agents-remediation/pkg/validation"
)

var _ = Describe("FenceAgentsRemediationTemplate Validation", func() {

	const (
		validAgentName   = "fence_ipmilan"
		invalidAgentName = "fence_ip"
	)

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
				Expect(err.Error()).To(ContainSubstring(validation.ErrorNotFoundAgent, invalidAgentName))
			})
		})
	})

	Context("updating FenceAgentsRemediationTemplate", func() {

		oldFARTemplate := getTestFARTemplate(invalidAgentName)
		When("agent name match format and binary", func() {
			It("should be accepted", func() {
				farTemplate := getTestFARTemplate(validAgentName)
				_, err := farTemplate.ValidateUpdate(oldFARTemplate)
				Expect(err).ToNot(HaveOccurred())
			})
		})

		When("agent name was not found ", func() {
			It("should be rejected", func() {
				farTemplate := getTestFARTemplate(invalidAgentName)
				_, err := farTemplate.ValidateUpdate(oldFARTemplate)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(validation.ErrorNotFoundAgent, invalidAgentName))
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
