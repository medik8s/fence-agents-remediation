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
				Expect(farTemplate.ValidateCreate()).Error().NotTo(HaveOccurred())
			})
		})

		When("agent name was not found ", func() {
			It("should be rejected", func() {
				farTemplate := getTestFARTemplate(invalidAgentName)
				warnings, err := farTemplate.ValidateCreate()
				ExpectWithOffset(1, warnings).To(BeEmpty())
				Expect(err).To(MatchError(ContainSubstring("unsupported fence agent: %s", invalidAgentName)))
			})
		})

		Context("with OutOfServiceTaint strategy", func() {
			var outOfServiceStrategy *FenceAgentsRemediationTemplate

			BeforeEach(func() {
				orgValue := isOutOfServiceTaintSupported
				DeferCleanup(func() { isOutOfServiceTaintSupported = orgValue })

				outOfServiceStrategy = getFARTemplate(validAgentName, OutOfServiceTaintRemediationStrategy)
			})

			When("out of service taint is supported", func() {
				BeforeEach(func() {
					isOutOfServiceTaintSupported = true
				})
				It("should be allowed", func() {
					Expect(outOfServiceStrategy.ValidateCreate()).Error().NotTo(HaveOccurred())
				})
			})

			When("out of service taint is not supported", func() {
				BeforeEach(func() {
					isOutOfServiceTaintSupported = false
				})
				It("should be denied", func() {
					warnings, err := outOfServiceStrategy.ValidateCreate()
					ExpectWithOffset(1, warnings).To(BeEmpty())
					Expect(err).To(MatchError(ContainSubstring(outOfServiceTaintUnsupportedMsg)))
				})
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
				Expect(farTemplate.ValidateUpdate(oldFARTemplate)).Error().NotTo(HaveOccurred())
			})
		})

		When("agent name was not found ", func() {
			BeforeEach(func() {
				oldFARTemplate = getTestFARTemplate(invalidAgentName)
			})
			It("should be rejected", func() {
				farTemplate := getTestFARTemplate(invalidAgentName)
				warnings, err := farTemplate.ValidateUpdate(oldFARTemplate)
				ExpectWithOffset(1, warnings).To(BeEmpty())
				Expect(err).To(MatchError(ContainSubstring("unsupported fence agent: %s", invalidAgentName)))
			})
		})

		Context("with OutOfServiceTaint strategy", func() {
			var outOfServiceStrategy *FenceAgentsRemediationTemplate
			var resourceDeletionStrategy *FenceAgentsRemediationTemplate

			BeforeEach(func() {
				orgValue := isOutOfServiceTaintSupported
				DeferCleanup(func() { isOutOfServiceTaintSupported = orgValue })

				outOfServiceStrategy = getFARTemplate(validAgentName, OutOfServiceTaintRemediationStrategy)
				resourceDeletionStrategy = getFARTemplate(validAgentName, ResourceDeletionRemediationStrategy)
			})

			When("out of service taint is supported", func() {
				BeforeEach(func() {
					isOutOfServiceTaintSupported = true
				})
				It("should be allowed", func() {
					Expect(outOfServiceStrategy.ValidateUpdate(resourceDeletionStrategy)).Error().NotTo(HaveOccurred())
				})
			})

			When("out of service taint is not supported", func() {
				BeforeEach(func() {
					isOutOfServiceTaintSupported = false
				})
				It("should be denied", func() {
					warnings, err := outOfServiceStrategy.ValidateUpdate(resourceDeletionStrategy)
					ExpectWithOffset(1, warnings).To(BeEmpty())
					Expect(err).To(MatchError(ContainSubstring(outOfServiceTaintUnsupportedMsg)))
				})
			})
		})
	})

	Context("validating template syntax", func() {
		It("should aggregate multiple template validation errors", func() {
			// Create a template with multiple invalid template strings
			farTemplate := &FenceAgentsRemediationTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "invalid-template",
					Namespace: "test-namespace",
				},
				Spec: FenceAgentsRemediationTemplateSpec{
					Template: FenceAgentsRemediationTemplateResource{
						Spec: FenceAgentsRemediationSpec{
							Agent: validAgentName,
							SharedParameters: map[ParameterName]string{
								"--systems-uri": "/redfish/v1/Systems/{{.NodeName", // Missing closing brace
								"--hostname":    "{{.InvalidField}}",               // Invalid field
								"--port":        "{{.NodeName}}.com",               // Valid template
								"--invalid":     "/path/{{.NodeName",               // Another missing closing brace
							},
						},
					},
				},
			}

			// Validate and expect aggregated errors
			warnings, err := farTemplate.ValidateCreate()
			Expect(warnings).To(BeEmpty())
			Expect(err).To(HaveOccurred())

			// Check that the error message contains information about multiple failures
			errorMsg := err.Error()
			Expect(errorMsg).To(ContainSubstring("--systems-uri"))
			Expect(errorMsg).To(ContainSubstring("--hostname"))
			Expect(errorMsg).To(ContainSubstring("--invalid"))
			// The valid parameter should not appear in error message
			Expect(errorMsg).ToNot(ContainSubstring("--port"))
		})

		It("should succeed when all templates are valid", func() {
			farTemplate := &FenceAgentsRemediationTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "valid-template",
					Namespace: "test-namespace",
				},
				Spec: FenceAgentsRemediationTemplateSpec{
					Template: FenceAgentsRemediationTemplateResource{
						Spec: FenceAgentsRemediationSpec{
							Agent: validAgentName,
							SharedParameters: map[ParameterName]string{
								"--systems-uri": "/redfish/v1/Systems/{{.NodeName}}",
								"--hostname":    "{{.NodeName}}.example.com",
								"--port":        "623", // No template, should be fine
							},
						},
					},
				},
			}

			warnings, err := farTemplate.ValidateCreate()
			Expect(err).ToNot(HaveOccurred())
			Expect(warnings).To(BeEmpty())
		})
	})
})

func getTestFARTemplate(agentName string) *FenceAgentsRemediationTemplate {
	return getFARTemplate(agentName, ResourceDeletionRemediationStrategy)
}

func getFARTemplate(agentName string, strategy RemediationStrategyType) *FenceAgentsRemediationTemplate {
	return &FenceAgentsRemediationTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-" + agentName + "-template",
		},
		Spec: FenceAgentsRemediationTemplateSpec{
			Template: FenceAgentsRemediationTemplateResource{
				Spec: FenceAgentsRemediationSpec{
					Agent:               agentName,
					RemediationStrategy: strategy,
				},
			},
		},
	}
}
