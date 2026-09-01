package v1alpha1

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	remediationv1alpha1 "github.com/medik8s/fence-agents-remediation/v5/api/v1alpha1"
)

const testNs = "test-namespace"

// getFuncNodeSecretIpConflict returns the default Get function behavior for secrets
func getFuncNodeSecretIpConflict() func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	return func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
		// Default behavior - Return a pre-built secret for testing duplicate parameters
		if key.Name == "test-node-secret-ip-conflict" && key.Namespace == testNs {
			if secret, ok := obj.(*corev1.Secret); ok {
				secret.ObjectMeta = metav1.ObjectMeta{
					Name:      "test-node-secret-ip-conflict",
					Namespace: testNs,
				}
				secret.Data = map[string][]byte{
					"--ip":       []byte("192.168.1.100"), // This will conflict with NodeParameters
					"--username": []byte("admin"),
				}
				return nil
			}
		}
		// Return NotFound error for any other secret to simulate missing secrets
		return apierrors.NewNotFound(schema.GroupResource{}, key.Name)
	}
}

var _ = Describe("FenceAgentsRemediationTemplate Validation", func() {

	Context("creating FenceAgentsRemediationTemplate", func() {

		When("agent name match format and binary", func() {
			It("should be accepted", func() {
				farTemplate := getFARTemplate(validAgentName, remediationv1alpha1.ResourceDeletionRemediationStrategy)
				_, err := farTemplateValidator.ValidateCreate(ctx, farTemplate)
				Expect(err).NotTo(HaveOccurred())
			})
		})

		When("farTemplate has only shared parameters without NodeTemplate and no node parameters", func() {
			It("should be rejected", func() {
				farTemplate := getFARTemplate(validAgentName, remediationv1alpha1.ResourceDeletionRemediationStrategy)
				farTemplate.Spec.Template.Spec.SharedParameters = map[remediationv1alpha1.ParameterName]string{
					"ip":       "192.168.1.100",
					"username": "admin",
					"password": "secret",
				}
				// Explicitly ensure no node parameters
				farTemplate.Spec.Template.Spec.NodeParameters = nil

				warnings, err := farTemplateValidator.ValidateCreate(ctx, farTemplate)
				Expect(warnings).To(BeEmpty())
				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError(ContainSubstring("invalid spec: mandatory parameters are missing")))
			})
		})

		When("farTemplate has only shared parameters with NodeTemplate and no node parameters", func() {
			It("should be accepted", func() {
				farTemplate := getFARTemplate(validAgentName, remediationv1alpha1.ResourceDeletionRemediationStrategy)
				farTemplate.Spec.Template.Spec.SharedParameters = map[remediationv1alpha1.ParameterName]string{
					"ip":       "192.168.1.100",
					"username": "admin",
					"password": "secret-{{.NodeName}}", // This contains a NodeTemplate
				}
				// Explicitly ensure no node parameters
				farTemplate.Spec.Template.Spec.NodeParameters = nil

				warnings, err := farTemplateValidator.ValidateCreate(ctx, farTemplate)
				Expect(err).NotTo(HaveOccurred())
				Expect(warnings).To(BeEmpty())
			})
		})

		When("farTemplate has only secret parameters with NodeTemplate and no node parameters", func() {
			It("should be accepted", func() {
				// Setup mock to return secret with NodeTemplate
				originalGetFunc := mockValidatorClient.GetFunc
				DeferCleanup(func() {
					mockValidatorClient.GetFunc = originalGetFunc
				})

				mockValidatorClient.GetFunc = func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if key.Name == "test-shared-secret-with-template" && key.Namespace == testNs {
						if secret, ok := obj.(*corev1.Secret); ok {
							secret.ObjectMeta = metav1.ObjectMeta{
								Name:      "test-shared-secret-with-template",
								Namespace: testNs,
							}
							secret.Data = map[string][]byte{
								"--ip":       []byte("192.168.1.{{.NodeName}}"), // This contains a NodeTemplate
								"--username": []byte("admin"),
								"--password": []byte("secret"),
							}
							return nil
						}
					}
					return apierrors.NewNotFound(schema.GroupResource{}, key.Name)
				}

				farTemplate := &remediationv1alpha1.FenceAgentsRemediationTemplate{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-secret-template",
						Namespace: testNs,
					},
					Spec: remediationv1alpha1.FenceAgentsRemediationTemplateSpec{
						Template: remediationv1alpha1.FenceAgentsRemediationTemplateResource{
							Spec: remediationv1alpha1.FenceAgentsRemediationSpec{
								Agent:               validAgentName,
								RemediationStrategy: remediationv1alpha1.ResourceDeletionRemediationStrategy,
								SharedSecretName:    ptr.To("test-shared-secret-with-template"),
								// Explicitly ensure no node parameters or shared parameters
								NodeParameters:   nil,
								SharedParameters: nil,
							},
						},
					},
				}

				warnings, err := farTemplateValidator.ValidateCreate(ctx, farTemplate)
				Expect(err).NotTo(HaveOccurred())
				Expect(warnings).To(BeEmpty())
			})
		})

		When("farTemplate has no shared parameters and no node parameters", func() {
			It("should be rejected", func() {
				farTemplate := &remediationv1alpha1.FenceAgentsRemediationTemplate{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-" + validAgentName + "-template",
					},
					Spec: remediationv1alpha1.FenceAgentsRemediationTemplateSpec{
						Template: remediationv1alpha1.FenceAgentsRemediationTemplateResource{
							Spec: remediationv1alpha1.FenceAgentsRemediationSpec{
								Agent:               validAgentName,
								RemediationStrategy: remediationv1alpha1.ResourceDeletionRemediationStrategy,
								// Explicitly no SharedParameters or NodeParameters
							},
						},
					},
				}
				warnings, err := farTemplateValidator.ValidateCreate(ctx, farTemplate)
				ExpectWithOffset(1, warnings).To(BeEmpty())
				Expect(err).To(MatchError(ContainSubstring("invalid spec: mandatory parameters are missing")))
			})
		})

		When("agent name was not found ", func() {
			It("should be rejected", func() {
				farTemplate := getFARTemplate(invalidAgentName, remediationv1alpha1.ResourceDeletionRemediationStrategy)
				warnings, err := farTemplateValidator.ValidateCreate(ctx, farTemplate)
				ExpectWithOffset(1, warnings).To(BeEmpty())
				Expect(err).To(MatchError(ContainSubstring("unsupported fence agent: %s", invalidAgentName)))
			})
		})

		Context("with OutOfServiceTaint strategy", func() {
			var outOfServiceStrategy *remediationv1alpha1.FenceAgentsRemediationTemplate

			BeforeEach(func() {
				orgValue := remediationv1alpha1.IsOutOfServiceTaintSupported
				DeferCleanup(func() { remediationv1alpha1.IsOutOfServiceTaintSupported = orgValue })

				outOfServiceStrategy = getFARTemplate(validAgentName, remediationv1alpha1.OutOfServiceTaintRemediationStrategy)
			})

			When("out of service taint is supported", func() {
				BeforeEach(func() {
					remediationv1alpha1.IsOutOfServiceTaintSupported = true
				})
				It("should be allowed", func() {
					_, err := farTemplateValidator.ValidateCreate(ctx, outOfServiceStrategy)
					Expect(err).NotTo(HaveOccurred())
				})
			})

			When("out of service taint is not supported", func() {
				BeforeEach(func() {
					remediationv1alpha1.IsOutOfServiceTaintSupported = false
				})
				It("should be denied", func() {
					warnings, err := farTemplateValidator.ValidateCreate(ctx, outOfServiceStrategy)
					ExpectWithOffset(1, warnings).To(BeEmpty())
					Expect(err).To(MatchError(ContainSubstring(outOfServiceTaintUnsupportedMsg)))
				})
			})
		})
	})

	Context("updating FenceAgentsRemediationTemplate", func() {
		var oldFARTemplate *remediationv1alpha1.FenceAgentsRemediationTemplate
		When("agent name match format and binary", func() {
			BeforeEach(func() {
				oldFARTemplate = getFARTemplate(invalidAgentName, remediationv1alpha1.ResourceDeletionRemediationStrategy)
			})
			It("should be accepted", func() {
				farTemplate := getFARTemplate(validAgentName, remediationv1alpha1.ResourceDeletionRemediationStrategy)
				_, err := farTemplateValidator.ValidateUpdate(ctx, oldFARTemplate, farTemplate)
				Expect(err).NotTo(HaveOccurred())
			})
		})

		When("agent name was not found ", func() {
			BeforeEach(func() {
				oldFARTemplate = getFARTemplate(invalidAgentName, remediationv1alpha1.ResourceDeletionRemediationStrategy)
			})
			It("should be rejected", func() {
				farTemplate := getFARTemplate(invalidAgentName, remediationv1alpha1.ResourceDeletionRemediationStrategy)
				warnings, err := farTemplateValidator.ValidateUpdate(ctx, oldFARTemplate, farTemplate)
				ExpectWithOffset(1, warnings).To(BeEmpty())
				Expect(err).To(MatchError(ContainSubstring("unsupported fence agent: %s", invalidAgentName)))
			})
		})

		When("action parameter is invalid", func() {
			BeforeEach(func() {
				oldFARTemplate = getFARTemplate(validAgentName, remediationv1alpha1.ResourceDeletionRemediationStrategy)
			})
			It("should be rejected", func() {
				farTemplate := getFARTemplate(validAgentName, remediationv1alpha1.ResourceDeletionRemediationStrategy)
				farTemplate.Spec.Template.Spec.SharedParameters = map[remediationv1alpha1.ParameterName]string{
					"action": "shutdown", // Invalid action
				}
				warnings, err := farTemplateValidator.ValidateUpdate(ctx, oldFARTemplate, farTemplate)
				ExpectWithOffset(1, warnings).To(BeEmpty())
				Expect(err).To(MatchError(ContainSubstring("FAR doesn't support any other action than `reboot` or `off`")))
			})
		})

		Context("with OutOfServiceTaint strategy", func() {
			var outOfServiceStrategy *remediationv1alpha1.FenceAgentsRemediationTemplate
			var resourceDeletionStrategy *remediationv1alpha1.FenceAgentsRemediationTemplate

			BeforeEach(func() {
				orgValue := remediationv1alpha1.IsOutOfServiceTaintSupported
				DeferCleanup(func() { remediationv1alpha1.IsOutOfServiceTaintSupported = orgValue })

				outOfServiceStrategy = getFARTemplate(validAgentName, remediationv1alpha1.OutOfServiceTaintRemediationStrategy)
				resourceDeletionStrategy = getFARTemplate(validAgentName, remediationv1alpha1.ResourceDeletionRemediationStrategy)
			})

			When("out of service taint is supported", func() {
				BeforeEach(func() {
					remediationv1alpha1.IsOutOfServiceTaintSupported = true
				})
				It("should be allowed", func() {
					_, err := farTemplateValidator.ValidateUpdate(ctx, resourceDeletionStrategy, outOfServiceStrategy)
					Expect(err).NotTo(HaveOccurred())
				})
			})

			When("out of service taint is not supported", func() {
				BeforeEach(func() {
					remediationv1alpha1.IsOutOfServiceTaintSupported = false
				})
				It("should be denied", func() {
					warnings, err := farTemplateValidator.ValidateUpdate(ctx, resourceDeletionStrategy, outOfServiceStrategy)
					ExpectWithOffset(1, warnings).To(BeEmpty())
					Expect(err).To(MatchError(ContainSubstring(outOfServiceTaintUnsupportedMsg)))
				})
			})
		})

		Context("validateTemplateForSharedSecretDefaultName", func() {
			When("old template does not have SharedSecretName", func() {
				It("should be allowed", func() {
					oldTemplate := getFARTemplate(validAgentName, remediationv1alpha1.ResourceDeletionRemediationStrategy)
					oldTemplate.Spec.Template.Spec.SharedSecretName = nil

					newTemplate := getFARTemplate(validAgentName, remediationv1alpha1.ResourceDeletionRemediationStrategy)
					newTemplate.Spec.Template.Spec.SharedSecretName = nil

					_, err := farTemplateValidator.ValidateUpdate(ctx, oldTemplate, newTemplate)
					Expect(err).NotTo(HaveOccurred())
				})
			})

			When("old template has SharedSecretName but not the old default name", func() {
				It("should be allowed", func() {
					oldTemplate := getFARTemplate(validAgentName, remediationv1alpha1.ResourceDeletionRemediationStrategy)
					oldTemplate.Spec.Template.Spec.SharedSecretName = ptr.To("some-other-secret")

					newTemplate := getFARTemplate(validAgentName, remediationv1alpha1.ResourceDeletionRemediationStrategy)
					newTemplate.Spec.Template.Spec.SharedSecretName = nil

					_, err := farTemplateValidator.ValidateUpdate(ctx, oldTemplate, newTemplate)
					Expect(err).NotTo(HaveOccurred())
				})
			})

			When("old template has old default name and new template still has a non-empty SharedSecretName", func() {

				const otherSecretName = "some-other-secret"

				BeforeEach(func() {
					originalGetFunc := mockValidatorClient.GetFunc
					DeferCleanup(func() {
						mockValidatorClient.GetFunc = originalGetFunc
					})
					mockValidatorClient.GetFunc = func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
						if key.Name == otherSecretName {
							if secret, ok := obj.(*corev1.Secret); ok {
								secret.ObjectMeta = metav1.ObjectMeta{
									Name:      otherSecretName,
									Namespace: testNs,
								}
								return nil
							}
						}
						return apierrors.NewNotFound(schema.GroupResource{}, key.Name)
					}
				})

				It("should be allowed", func() {
					oldTemplate := getFARTemplate(validAgentName, remediationv1alpha1.ResourceDeletionRemediationStrategy)
					oldTemplate.Spec.Template.Spec.SharedSecretName = ptr.To(remediationv1alpha1.OldDefaultSecretName)

					newTemplate := getFARTemplate(validAgentName, remediationv1alpha1.ResourceDeletionRemediationStrategy)
					newTemplate.Spec.Template.Spec.SharedSecretName = ptr.To(otherSecretName)

					_, err := farTemplateValidator.ValidateUpdate(ctx, oldTemplate, newTemplate)
					Expect(err).NotTo(HaveOccurred())
				})
			})

			When("old template has old default name, new template removes it, and secret does not exist", func() {
				BeforeEach(func() {
					originalGetFunc := mockValidatorClient.GetFunc
					DeferCleanup(func() {
						mockValidatorClient.GetFunc = originalGetFunc
					})
					mockValidatorClient.GetFunc = func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
						return apierrors.NewNotFound(schema.GroupResource{}, key.Name)
					}
				})

				It("should be allowed", func() {
					oldTemplate := getFARTemplate(validAgentName, remediationv1alpha1.ResourceDeletionRemediationStrategy)
					oldTemplate.ObjectMeta.Namespace = testNs
					oldTemplate.Spec.Template.Spec.SharedSecretName = ptr.To(remediationv1alpha1.OldDefaultSecretName)

					newTemplate := getFARTemplate(validAgentName, remediationv1alpha1.ResourceDeletionRemediationStrategy)
					newTemplate.ObjectMeta.Namespace = testNs
					newTemplate.Spec.Template.Spec.SharedSecretName = nil

					_, err := farTemplateValidator.ValidateUpdate(ctx, oldTemplate, newTemplate)
					Expect(err).NotTo(HaveOccurred())
				})
			})

			When("old template has old default name, new template removes it, and secret exists", func() {
				BeforeEach(func() {
					originalGetFunc := mockValidatorClient.GetFunc
					DeferCleanup(func() {
						mockValidatorClient.GetFunc = originalGetFunc
					})
					mockValidatorClient.GetFunc = func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
						if key.Name == remediationv1alpha1.OldDefaultSecretName {
							if secret, ok := obj.(*corev1.Secret); ok {
								secret.ObjectMeta = metav1.ObjectMeta{
									Name:      remediationv1alpha1.OldDefaultSecretName,
									Namespace: testNs,
								}
								return nil
							}
						}
						return apierrors.NewNotFound(schema.GroupResource{}, key.Name)
					}
				})

				It("should be rejected", func() {
					oldTemplate := getFARTemplate(validAgentName, remediationv1alpha1.ResourceDeletionRemediationStrategy)
					oldTemplate.ObjectMeta.Namespace = testNs
					oldTemplate.Spec.Template.Spec.SharedSecretName = ptr.To(remediationv1alpha1.OldDefaultSecretName)

					newTemplate := getFARTemplate(validAgentName, remediationv1alpha1.ResourceDeletionRemediationStrategy)
					newTemplate.ObjectMeta.Namespace = testNs
					newTemplate.Spec.Template.Spec.SharedSecretName = nil

					_, err := farTemplateValidator.ValidateUpdate(ctx, oldTemplate, newTemplate)
					Expect(err).To(HaveOccurred())
					Expect(err.Error()).To(ContainSubstring("shared secret with the deprecated default name"))
					Expect(err.Error()).To(ContainSubstring(remediationv1alpha1.OldDefaultSecretName))
				})
			})

			When("old template has old default name, new template sets empty string, and secret exists", func() {
				BeforeEach(func() {
					originalGetFunc := mockValidatorClient.GetFunc
					DeferCleanup(func() {
						mockValidatorClient.GetFunc = originalGetFunc
					})
					mockValidatorClient.GetFunc = func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
						if key.Name == remediationv1alpha1.OldDefaultSecretName {
							if secret, ok := obj.(*corev1.Secret); ok {
								secret.ObjectMeta = metav1.ObjectMeta{
									Name:      remediationv1alpha1.OldDefaultSecretName,
									Namespace: testNs,
								}
								return nil
							}
						}
						return apierrors.NewNotFound(schema.GroupResource{}, key.Name)
					}
				})

				It("should be rejected", func() {
					oldTemplate := getFARTemplate(validAgentName, remediationv1alpha1.ResourceDeletionRemediationStrategy)
					oldTemplate.ObjectMeta.Namespace = testNs
					oldTemplate.Spec.Template.Spec.SharedSecretName = ptr.To(remediationv1alpha1.OldDefaultSecretName)

					newTemplate := getFARTemplate(validAgentName, remediationv1alpha1.ResourceDeletionRemediationStrategy)
					newTemplate.ObjectMeta.Namespace = testNs
					newTemplate.Spec.Template.Spec.SharedSecretName = ptr.To("")

					_, err := farTemplateValidator.ValidateUpdate(ctx, oldTemplate, newTemplate)
					Expect(err).To(HaveOccurred())
					Expect(err.Error()).To(ContainSubstring("shared secret with the deprecated default name"))
				})
			})

			When("fetching secret fails with unexpected error", func() {
				BeforeEach(func() {
					originalGetFunc := mockValidatorClient.GetFunc
					DeferCleanup(func() {
						mockValidatorClient.GetFunc = originalGetFunc
					})
					mockValidatorClient.GetFunc = func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
						if key.Name == remediationv1alpha1.OldDefaultSecretName {
							return apierrors.NewInternalError(fmt.Errorf("unexpected error"))
						}
						return apierrors.NewNotFound(schema.GroupResource{}, key.Name)
					}
				})

				It("should return an error asking to retry", func() {
					oldTemplate := getFARTemplate(validAgentName, remediationv1alpha1.ResourceDeletionRemediationStrategy)
					oldTemplate.ObjectMeta.Namespace = testNs
					oldTemplate.Spec.Template.Spec.SharedSecretName = ptr.To(remediationv1alpha1.OldDefaultSecretName)

					newTemplate := getFARTemplate(validAgentName, remediationv1alpha1.ResourceDeletionRemediationStrategy)
					newTemplate.ObjectMeta.Namespace = testNs
					newTemplate.Spec.Template.Spec.SharedSecretName = nil

					_, err := farTemplateValidator.ValidateUpdate(ctx, oldTemplate, newTemplate)
					Expect(err).To(HaveOccurred())
					Expect(err.Error()).To(ContainSubstring("failed to check if the default shared secret exists"))
				})
			})
		})
	})

	Context("validating template syntax", func() {
		It("should aggregate multiple template validation errors", func() {
			// Create a template with multiple invalid template strings
			farTemplate := &remediationv1alpha1.FenceAgentsRemediationTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "invalid-template",
					Namespace: testNs,
				},
				Spec: remediationv1alpha1.FenceAgentsRemediationTemplateSpec{
					Template: remediationv1alpha1.FenceAgentsRemediationTemplateResource{
						Spec: remediationv1alpha1.FenceAgentsRemediationSpec{
							Agent: validAgentName,
							SharedParameters: map[remediationv1alpha1.ParameterName]string{
								"--systems-uri": "/redfish/v1/Systems/{{.NodeName", // Missing closing brace
								"--hostname":    "{{.InvalidField}}",               // Unsupported name, only remediationv1alpha1.NodeName is supported
								"--port":        "{{.NodeName}}.com",               // Valid template
								"--invalid":     "/path/{{.NodeName",               // Another missing closing brace
							},
						},
					},
				},
			}

			// Validate and expect aggregated errors
			warnings, err := farTemplateValidator.ValidateCreate(ctx, farTemplate)
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
			farTemplate := &remediationv1alpha1.FenceAgentsRemediationTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "valid-template",
					Namespace: testNs,
				},
				Spec: remediationv1alpha1.FenceAgentsRemediationTemplateSpec{
					Template: remediationv1alpha1.FenceAgentsRemediationTemplateResource{
						Spec: remediationv1alpha1.FenceAgentsRemediationSpec{
							Agent: validAgentName,
							SharedParameters: map[remediationv1alpha1.ParameterName]string{
								"--systems-uri": "/redfish/v1/Systems/{{.NodeName}}",
								"--hostname":    "{{.NodeName}}.example.com",
								"--port":        "623", // No template, should be fine
							},
						},
					},
				},
			}

			warnings, err := farTemplateValidator.ValidateCreate(ctx, farTemplate)
			Expect(err).ToNot(HaveOccurred())
			Expect(warnings).To(BeEmpty())
		})
	})

	Context("validating parameter validation functionality", func() {
		BeforeEach(func() {
			originalGetFunc := mockValidatorClient.GetFunc
			DeferCleanup(func() {
				mockValidatorClient.GetFunc = originalGetFunc
			})
			// Set up default secret behavior for tests that need it
			mockValidatorClient.GetFunc = getFuncNodeSecretIpConflict()
		})

		It("should fail when template has invalid action parameter", func() {
			farTemplate := &remediationv1alpha1.FenceAgentsRemediationTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "invalid-action-template",
					Namespace: testNs,
				},
				Spec: remediationv1alpha1.FenceAgentsRemediationTemplateSpec{
					Template: remediationv1alpha1.FenceAgentsRemediationTemplateResource{
						Spec: remediationv1alpha1.FenceAgentsRemediationSpec{
							Agent: validAgentName,
							SharedParameters: map[remediationv1alpha1.ParameterName]string{
								"--ip":     "192.168.1.100",
								"--action": "shutdown", // Invalid action - only "reboot" or "off" are supported
							},
						},
					},
				},
			}

			warnings, err := farTemplateValidator.ValidateCreate(ctx, farTemplate)
			Expect(warnings).To(BeEmpty())
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("FAR doesn't support any other action than `reboot`"))
		})

		It("should fail when templates reference missing node secrets", func() {
			farTemplate := &remediationv1alpha1.FenceAgentsRemediationTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "missing-secrets-template",
					Namespace: testNs,
				},
				Spec: remediationv1alpha1.FenceAgentsRemediationTemplateSpec{
					Template: remediationv1alpha1.FenceAgentsRemediationTemplateResource{
						Spec: remediationv1alpha1.FenceAgentsRemediationSpec{
							Agent: validAgentName,
							SharedParameters: map[remediationv1alpha1.ParameterName]string{
								"--ip": "192.168.1.100",
							},
							NodeSecretNames: map[remediationv1alpha1.NodeName]string{
								"worker-1": "non-existent-node-secret",
							},
						},
					},
				},
			}

			warnings, err := farTemplateValidator.ValidateCreate(ctx, farTemplate)
			// Should fail because node secrets are expected to exist when referenced
			Expect(warnings).To(BeEmpty())
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("secret 'non-existent-node-secret' not found in namespace 'test-namespace'"))
		})

		It("should fail when template references a missing shared secret", func() {
			farTemplate := &remediationv1alpha1.FenceAgentsRemediationTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "missing-shared-secret-template",
					Namespace: testNs,
				},
				Spec: remediationv1alpha1.FenceAgentsRemediationTemplateSpec{
					Template: remediationv1alpha1.FenceAgentsRemediationTemplateResource{
						Spec: remediationv1alpha1.FenceAgentsRemediationSpec{
							Agent:            validAgentName,
							SharedSecretName: ptr.To("non-existent-shared-secret"),
						},
					},
				},
			}

			warnings, err := farTemplateValidator.ValidateCreate(ctx, farTemplate)
			Expect(warnings).To(BeEmpty())
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("secret 'non-existent-shared-secret' not found in namespace 'test-namespace'"))
		})

		It("should fail when NodeSecretParam duplicates a NodeParam", func() {
			farTemplate := &remediationv1alpha1.FenceAgentsRemediationTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "duplicate-params-template",
					Namespace: testNs,
				},
				Spec: remediationv1alpha1.FenceAgentsRemediationTemplateSpec{
					Template: remediationv1alpha1.FenceAgentsRemediationTemplateResource{
						Spec: remediationv1alpha1.FenceAgentsRemediationSpec{
							Agent: validAgentName,
							NodeParameters: map[remediationv1alpha1.ParameterName]map[remediationv1alpha1.NodeName]string{
								"--ip": {
									"worker-1": "192.168.1.101", // This will conflict with secret
								},
								"--port": {
									"worker-1": "623",
								},
							},
							NodeSecretNames: map[remediationv1alpha1.NodeName]string{
								"worker-1": "test-node-secret-ip-conflict", // This secret contains "--ip" parameter
							},
						},
					},
				},
			}

			warnings, err := farTemplateValidator.ValidateCreate(ctx, farTemplate)
			// Should fail because "--ip" is defined in both NodeParameters and the secret
			Expect(warnings).To(BeEmpty())
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid multiple definition of FAR parameter"))
		})

	})

	Context("validating StatusValidationSample", func() {

		var fart *remediationv1alpha1.FenceAgentsRemediationTemplate

		BeforeEach(func() {
			fart = getTestFART()
		})

		AfterEach(func() {
			_ = k8sClient.Delete(context.Background(), fart)
			Eventually(func() bool {
				if err := k8sClient.Get(ctx, types.NamespacedName{Name: fart.Name, Namespace: fart.Namespace}, fart); apierrors.IsNotFound(err) {
					return true
				}
				return false
			}).Should(BeTrue())
		})

		When("StatusValidationSample is nil", func() {
			It("should be accepted", func() {
				fart.Spec.StatusValidationSample = nil
				Expect(k8sClient.Create(context.Background(), fart)).To(Succeed())
			})
		})

		When("StatusValidationSample is a number string without percentage", func() {
			It("should be rejected", func() {
				fart.Spec.StatusValidationSample = ptr.To(intstr.IntOrString{Type: intstr.String, StrVal: "10"})
				Expect(k8sClient.Create(context.Background(), fart)).ToNot(Succeed())
			})
		})

		When("StatusValidationSample is a percentage with leading 0", func() {
			It("should be rejected", func() {
				fart.Spec.StatusValidationSample = ptr.To(intstr.IntOrString{Type: intstr.String, StrVal: "05%"})
				Expect(k8sClient.Create(context.Background(), fart)).ToNot(Succeed())
			})
		})

		When("StatusValidationSample is a random string", func() {
			It("should be rejected", func() {
				fart.Spec.StatusValidationSample = ptr.To(intstr.IntOrString{Type: intstr.String, StrVal: "string"})
				Expect(k8sClient.Create(context.Background(), fart)).ToNot(Succeed())
			})
		})

		When("StatusValidationSample is a negative percentage", func() {
			It("should be rejected", func() {
				fart.Spec.StatusValidationSample = ptr.To(intstr.IntOrString{Type: intstr.String, StrVal: "-42%"})
				Expect(k8sClient.Create(context.Background(), fart)).ToNot(Succeed())
			})
		})

		When("StatusValidationSample is valid 1 digit percentage", func() {
			It("should be accepted", func() {
				fart.Spec.StatusValidationSample = ptr.To(intstr.IntOrString{Type: intstr.String, StrVal: "5%"})
				Expect(k8sClient.Create(context.Background(), fart)).To(Succeed())
			})
		})

		When("StatusValidationSample is valid 2 digit percentage", func() {
			It("should be accepted", func() {
				fart.Spec.StatusValidationSample = ptr.To(intstr.IntOrString{Type: intstr.String, StrVal: "55%"})
				Expect(k8sClient.Create(context.Background(), fart)).To(Succeed())
			})
		})

		When("StatusValidationSample is valid 3 digit percentage", func() {
			It("should be accepted", func() {
				fart.Spec.StatusValidationSample = ptr.To(intstr.IntOrString{Type: intstr.String, StrVal: "100%"})
				Expect(k8sClient.Create(context.Background(), fart)).To(Succeed())
			})
		})

		When("StatusValidationSample is invalid 3 digit percentage", func() {
			It("should be rejected", func() {
				fart.Spec.StatusValidationSample = ptr.To(intstr.IntOrString{Type: intstr.String, StrVal: "101%"})
				Expect(k8sClient.Create(context.Background(), fart)).ToNot(Succeed())
			})
		})

		When("StatusValidationSample is a negative int", func() {
			It("should be rejected", func() {
				fart.Spec.StatusValidationSample = ptr.To(intstr.IntOrString{Type: intstr.Int, IntVal: -5})
				Expect(k8sClient.Create(context.Background(), fart)).ToNot(Succeed())
			})
		})

		When("StatusValidationSample is a 0", func() {
			It("should be accepted", func() {
				fart.Spec.StatusValidationSample = ptr.To(intstr.IntOrString{Type: intstr.Int, IntVal: 0})
				Expect(k8sClient.Create(context.Background(), fart)).To(Succeed())
			})
		})

		When("StatusValidationSample is a positive int", func() {
			It("should be accepted", func() {
				fart.Spec.StatusValidationSample = ptr.To(intstr.IntOrString{Type: intstr.Int, IntVal: 105})
				Expect(k8sClient.Create(context.Background(), fart)).To(Succeed())
			})
		})
	})
})

func getTestFART() *remediationv1alpha1.FenceAgentsRemediationTemplate {
	return &remediationv1alpha1.FenceAgentsRemediationTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-fart",
			Namespace: metav1.NamespaceDefault,
		}, Spec: remediationv1alpha1.FenceAgentsRemediationTemplateSpec{Template: remediationv1alpha1.FenceAgentsRemediationTemplateResource{Spec: getTestFAR(validAgentName).Spec}}}
}

var _ = Describe("FenceAgentsRemediationTemplate Defaulting", func() {

	var defaulter *farTemplateDefaulter

	BeforeEach(func() {
		defaulter = &farTemplateDefaulter{
			Client: mockValidatorClient,
		}
	})

	Context("applySharedSecretDefaultName", func() {
		When("SharedSecretName is nil and the old default secret exists", func() {
			BeforeEach(func() {
				originalGetFunc := mockValidatorClient.GetFunc
				DeferCleanup(func() {
					mockValidatorClient.GetFunc = originalGetFunc
				})
				mockValidatorClient.GetFunc = func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if key.Name == remediationv1alpha1.OldDefaultSecretName {
						if secret, ok := obj.(*corev1.Secret); ok {
							secret.ObjectMeta = metav1.ObjectMeta{
								Name:      remediationv1alpha1.OldDefaultSecretName,
								Namespace: testNs,
							}
							return nil
						}
					}
					return apierrors.NewNotFound(schema.GroupResource{}, key.Name)
				}
			})

			It("should set SharedSecretName to the old default name", func() {
				farTemplate := getFARTemplate(validAgentName, remediationv1alpha1.ResourceDeletionRemediationStrategy)
				farTemplate.Spec.Template.Spec.SharedSecretName = nil

				err := defaulter.Default(ctx, farTemplate)
				Expect(err).NotTo(HaveOccurred())
				Expect(farTemplate.Spec.Template.Spec.SharedSecretName).NotTo(BeNil())
				Expect(*farTemplate.Spec.Template.Spec.SharedSecretName).To(Equal(remediationv1alpha1.OldDefaultSecretName))
			})
		})

		When("SharedSecretName is nil and the old default secret exists on update", func() {
			BeforeEach(func() {
				originalGetFunc := mockValidatorClient.GetFunc
				DeferCleanup(func() {
					mockValidatorClient.GetFunc = originalGetFunc
				})
				mockValidatorClient.GetFunc = func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if key.Name == remediationv1alpha1.OldDefaultSecretName {
						if secret, ok := obj.(*corev1.Secret); ok {
							secret.ObjectMeta = metav1.ObjectMeta{
								Name:      remediationv1alpha1.OldDefaultSecretName,
								Namespace: testNs,
							}
							return nil
						}
					}
					return apierrors.NewNotFound(schema.GroupResource{}, key.Name)
				}
			})

			It("should not set SharedSecretName", func() {
				farTemplate := getFARTemplate(validAgentName, remediationv1alpha1.ResourceDeletionRemediationStrategy)
				farTemplate.CreationTimestamp = metav1.Now() // simulate update
				farTemplate.Spec.Template.Spec.SharedSecretName = nil

				err := defaulter.Default(ctx, farTemplate)
				Expect(err).NotTo(HaveOccurred())
				Expect(farTemplate.Spec.Template.Spec.SharedSecretName).To(BeNil())
			})
		})

		When("SharedSecretName is the old default and the secret does not exist", func() {
			BeforeEach(func() {
				originalGetFunc := mockValidatorClient.GetFunc
				DeferCleanup(func() {
					mockValidatorClient.GetFunc = originalGetFunc
				})
				mockValidatorClient.GetFunc = func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					return apierrors.NewNotFound(schema.GroupResource{}, key.Name)
				}
			})

			It("should remove SharedSecretName", func() {
				farTemplate := getFARTemplate(validAgentName, remediationv1alpha1.ResourceDeletionRemediationStrategy)
				farTemplate.CreationTimestamp = metav1.Now() // simulate update
				farTemplate.Spec.Template.Spec.SharedSecretName = ptr.To(remediationv1alpha1.OldDefaultSecretName)

				err := defaulter.Default(ctx, farTemplate)
				Expect(err).NotTo(HaveOccurred())
				Expect(farTemplate.Spec.Template.Spec.SharedSecretName).To(BeNil())
			})
		})

		When("SharedSecretName is nil and the secret does not exist", func() {
			BeforeEach(func() {
				originalGetFunc := mockValidatorClient.GetFunc
				DeferCleanup(func() {
					mockValidatorClient.GetFunc = originalGetFunc
				})
				mockValidatorClient.GetFunc = func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					return apierrors.NewNotFound(schema.GroupResource{}, key.Name)
				}
			})

			It("should not modify SharedSecretName", func() {
				farTemplate := getFARTemplate(validAgentName, remediationv1alpha1.ResourceDeletionRemediationStrategy)
				farTemplate.Spec.Template.Spec.SharedSecretName = nil

				err := defaulter.Default(ctx, farTemplate)
				Expect(err).NotTo(HaveOccurred())
				Expect(farTemplate.Spec.Template.Spec.SharedSecretName).To(BeNil())
			})
		})

		When("SharedSecretName is a custom name and the old default secret exists", func() {
			BeforeEach(func() {
				originalGetFunc := mockValidatorClient.GetFunc
				DeferCleanup(func() {
					mockValidatorClient.GetFunc = originalGetFunc
				})
				mockValidatorClient.GetFunc = func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if key.Name == remediationv1alpha1.OldDefaultSecretName {
						if secret, ok := obj.(*corev1.Secret); ok {
							secret.ObjectMeta = metav1.ObjectMeta{
								Name:      remediationv1alpha1.OldDefaultSecretName,
								Namespace: testNs,
							}
							return nil
						}
					}
					return apierrors.NewNotFound(schema.GroupResource{}, key.Name)
				}
			})

			It("should not modify SharedSecretName", func() {
				farTemplate := getFARTemplate(validAgentName, remediationv1alpha1.ResourceDeletionRemediationStrategy)
				farTemplate.Spec.Template.Spec.SharedSecretName = ptr.To("my-custom-secret")

				err := defaulter.Default(ctx, farTemplate)
				Expect(err).NotTo(HaveOccurred())
				Expect(farTemplate.Spec.Template.Spec.SharedSecretName).NotTo(BeNil())
				Expect(*farTemplate.Spec.Template.Spec.SharedSecretName).To(Equal("my-custom-secret"))
			})
		})

		When("fetching the secret fails with an unexpected error", func() {
			BeforeEach(func() {
				originalGetFunc := mockValidatorClient.GetFunc
				DeferCleanup(func() {
					mockValidatorClient.GetFunc = originalGetFunc
				})
				mockValidatorClient.GetFunc = func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if key.Name == remediationv1alpha1.OldDefaultSecretName {
						return apierrors.NewInternalError(fmt.Errorf("unexpected error"))
					}
					return apierrors.NewNotFound(schema.GroupResource{}, key.Name)
				}
			})

			It("should return an error", func() {
				farTemplate := getFARTemplate(validAgentName, remediationv1alpha1.ResourceDeletionRemediationStrategy)
				farTemplate.Spec.Template.Spec.SharedSecretName = nil

				err := defaulter.Default(ctx, farTemplate)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failed to check for shared secret"))
			})
		})
	})

	Context("annotations defaulting", func() {
		When("MultipleTemplatesSupportedAnnotation is not set", func() {
			It("should set the annotation to true", func() {
				farTemplate := getFARTemplate(validAgentName, remediationv1alpha1.ResourceDeletionRemediationStrategy)
				farTemplate.Annotations = nil

				err := defaulter.Default(ctx, farTemplate)
				Expect(err).NotTo(HaveOccurred())
				Expect(farTemplate.Annotations).NotTo(BeNil())
			})
		})
	})

})

func getFARTemplate(agentName string, strategy remediationv1alpha1.RemediationStrategyType) *remediationv1alpha1.FenceAgentsRemediationTemplate {
	return &remediationv1alpha1.FenceAgentsRemediationTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-" + agentName + "-template",
			Namespace: testNs,
		},
		Spec: remediationv1alpha1.FenceAgentsRemediationTemplateSpec{
			Template: remediationv1alpha1.FenceAgentsRemediationTemplateResource{
				Spec: remediationv1alpha1.FenceAgentsRemediationSpec{
					Agent:               agentName,
					RemediationStrategy: strategy,
					// Add basic shared parameters with a template to satisfy new validation
					SharedParameters: map[remediationv1alpha1.ParameterName]string{
						"ip":       "192.168.1.100",
						"username": "admin-{{.NodeName}}", // Contains NodeTemplate to satisfy validation
					},
				},
			},
		},
	}
}
