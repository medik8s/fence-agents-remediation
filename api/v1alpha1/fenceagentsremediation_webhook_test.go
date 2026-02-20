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
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("FenceAgentsRemediation Validation", func() {

	Context("creating FenceAgentsRemediation", func() {

		When("agent name match format and binary", func() {
			It("should be accepted", func() {
				far := getTestFAR(validAgentName)
				Expect(validator.ValidateCreate(ctx, far)).Error().NotTo(HaveOccurred())
			})
		})

		When("agent name was not found ", func() {
			It("should be rejected", func() {
				far := getTestFAR(invalidAgentName)
				warnings, err := validator.ValidateCreate(ctx, far)
				ExpectWithOffset(1, warnings).To(BeEmpty())
				Expect(err).To(MatchError(ContainSubstring("unsupported fence agent: %s", invalidAgentName)))
			})
		})

		Context("with OutOfServiceTaint strategy", func() {
			var outOfServiceStrategy *FenceAgentsRemediation

			BeforeEach(func() {
				orgValue := isOutOfServiceTaintSupported
				DeferCleanup(func() { isOutOfServiceTaintSupported = orgValue })

				outOfServiceStrategy = getFAR(validAgentName, OutOfServiceTaintRemediationStrategy)
			})
			When("out of service taint is supported", func() {
				BeforeEach(func() {
					isOutOfServiceTaintSupported = true
				})
				It("should be allowed", func() {
					Expect(validator.ValidateCreate(ctx, outOfServiceStrategy)).Error().NotTo(HaveOccurred())
				})
			})
			When("out of service taint is not supported", func() {
				BeforeEach(func() {
					isOutOfServiceTaintSupported = false
				})
				It("should be denied", func() {
					warnings, err := validator.ValidateCreate(ctx, outOfServiceStrategy)
					ExpectWithOffset(1, warnings).To(BeEmpty())
					Expect(err).To(MatchError(ContainSubstring(outOfServiceTaintUnsupportedMsg)))
				})
			})
		})
	})

	Context("updating FenceAgentsRemediation", func() {
		var oldFAR *FenceAgentsRemediation
		When("agent name match format and binary", func() {
			BeforeEach(func() {
				oldFAR = getTestFAR(invalidAgentName)
			})
			It("should be accepted", func() {
				far := getTestFAR(validAgentName)
				Expect(validator.ValidateUpdate(ctx, oldFAR, far)).Error().NotTo(HaveOccurred())
			})
		})

		When("agent name was not found ", func() {
			BeforeEach(func() {
				oldFAR = getTestFAR(invalidAgentName)
			})
			It("should be rejected", func() {
				far := getTestFAR(invalidAgentName)
				warnings, err := validator.ValidateUpdate(ctx, oldFAR, far)
				ExpectWithOffset(1, warnings).To(BeEmpty())
				Expect(err).To(MatchError(ContainSubstring("unsupported fence agent: %s", invalidAgentName)))
			})
		})

		Context("with OutOfServiceTaint strategy", func() {
			var outOfServiceStrategy *FenceAgentsRemediation
			var resourceDeletionStrategy *FenceAgentsRemediation

			BeforeEach(func() {
				orgValue := isOutOfServiceTaintSupported
				DeferCleanup(func() { isOutOfServiceTaintSupported = orgValue })

				outOfServiceStrategy = getFAR(validAgentName, OutOfServiceTaintRemediationStrategy)
				resourceDeletionStrategy = getFAR(validAgentName, ResourceDeletionRemediationStrategy)
			})
			When("out of service taint is supported", func() {
				BeforeEach(func() {
					isOutOfServiceTaintSupported = true
				})
				It("should be allowed", func() {
					Expect(validator.ValidateUpdate(ctx, resourceDeletionStrategy, outOfServiceStrategy)).Error().NotTo(HaveOccurred())
				})
			})
			When("out of service taint is not supported", func() {
				BeforeEach(func() {
					isOutOfServiceTaintSupported = false
				})
				It("should be denied", func() {
					warnings, err := validator.ValidateUpdate(ctx, resourceDeletionStrategy, outOfServiceStrategy)
					ExpectWithOffset(1, warnings).To(BeEmpty())
					Expect(err).To(MatchError(ContainSubstring(outOfServiceTaintUnsupportedMsg)))
				})
			})
		})
	})
})

var _ = Describe("FenceAgentsRemediation Defaulting", func() {

	var defaulter *farDefaulter

	BeforeEach(func() {
		defaulter = &farDefaulter{
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
					if key.Name == OldDefaultSecretName {
						if secret, ok := obj.(*corev1.Secret); ok {
							secret.ObjectMeta = metav1.ObjectMeta{
								Name:      OldDefaultSecretName,
								Namespace: testNs,
							}
							return nil
						}
					}
					return apierrors.NewNotFound(schema.GroupResource{}, key.Name)
				}
			})

			It("should set SharedSecretName to the old default name", func() {
				far := getFAR(validAgentName, ResourceDeletionRemediationStrategy)
				far.Namespace = testNs
				far.Spec.SharedSecretName = nil

				err := defaulter.Default(ctx, far)
				Expect(err).NotTo(HaveOccurred())
				Expect(far.Spec.SharedSecretName).NotTo(BeNil())
				Expect(*far.Spec.SharedSecretName).To(Equal(OldDefaultSecretName))
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
				far := getFAR(validAgentName, ResourceDeletionRemediationStrategy)
				far.Namespace = testNs
				far.Spec.SharedSecretName = ptr.To(OldDefaultSecretName)

				err := defaulter.Default(ctx, far)
				Expect(err).NotTo(HaveOccurred())
				Expect(far.Spec.SharedSecretName).To(BeNil())
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
				far := getFAR(validAgentName, ResourceDeletionRemediationStrategy)
				far.Namespace = testNs
				far.Spec.SharedSecretName = nil

				err := defaulter.Default(ctx, far)
				Expect(err).NotTo(HaveOccurred())
				Expect(far.Spec.SharedSecretName).To(BeNil())
			})
		})

		When("SharedSecretName is a custom name and the old default secret exists", func() {
			BeforeEach(func() {
				originalGetFunc := mockValidatorClient.GetFunc
				DeferCleanup(func() {
					mockValidatorClient.GetFunc = originalGetFunc
				})
				mockValidatorClient.GetFunc = func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if key.Name == OldDefaultSecretName {
						if secret, ok := obj.(*corev1.Secret); ok {
							secret.ObjectMeta = metav1.ObjectMeta{
								Name:      OldDefaultSecretName,
								Namespace: testNs,
							}
							return nil
						}
					}
					return apierrors.NewNotFound(schema.GroupResource{}, key.Name)
				}
			})

			It("should not modify SharedSecretName", func() {
				far := getFAR(validAgentName, ResourceDeletionRemediationStrategy)
				far.Namespace = testNs
				far.Spec.SharedSecretName = ptr.To("my-custom-secret")

				err := defaulter.Default(ctx, far)
				Expect(err).NotTo(HaveOccurred())
				Expect(far.Spec.SharedSecretName).NotTo(BeNil())
				Expect(*far.Spec.SharedSecretName).To(Equal("my-custom-secret"))
			})
		})

		When("fetching the secret fails with an unexpected error", func() {
			BeforeEach(func() {
				originalGetFunc := mockValidatorClient.GetFunc
				DeferCleanup(func() {
					mockValidatorClient.GetFunc = originalGetFunc
				})
				mockValidatorClient.GetFunc = func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if key.Name == OldDefaultSecretName {
						return apierrors.NewInternalError(fmt.Errorf("unexpected error"))
					}
					return apierrors.NewNotFound(schema.GroupResource{}, key.Name)
				}
			})

			It("should return an error", func() {
				far := getFAR(validAgentName, ResourceDeletionRemediationStrategy)
				far.Namespace = testNs
				far.Spec.SharedSecretName = nil

				err := defaulter.Default(ctx, far)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failed to check for shared secret"))
			})
		})
	})

	Context("with wrong object type", func() {
		It("should return an error", func() {
			farTemplate := getFARTemplate(validAgentName, ResourceDeletionRemediationStrategy)
			err := defaulter.Default(ctx, farTemplate)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("expected a FenceAgentsRemediation"))
		})
	})
})

func getTestFAR(agentName string) *FenceAgentsRemediation {
	return getFAR(agentName, ResourceDeletionRemediationStrategy)
}

func getFAR(agentName string, strategy RemediationStrategyType) *FenceAgentsRemediation {
	return &FenceAgentsRemediation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-far",
			Namespace: metav1.NamespaceDefault,
		},
		Spec: FenceAgentsRemediationSpec{
			Agent:               agentName,
			RemediationStrategy: strategy,
			NodeParameters: map[ParameterName]map[NodeName]string{
				"--ipport": {"worker-0": "6230"},
			},
		},
	}
}
