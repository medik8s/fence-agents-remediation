/*
Copyright 2023.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package controller

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"
	commonAnnotations "github.com/medik8s/common/pkg/annotations"
	commonConditions "github.com/medik8s/common/pkg/conditions"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/medik8s/fence-agents-remediation/v5/api/v1alpha1"
	"github.com/medik8s/fence-agents-remediation/v5/pkg/cli"
	"github.com/medik8s/fence-agents-remediation/v5/pkg/utils"
)

const (
	dummyNode      = "dummy-node"
	workerNode     = "worker-0"
	fenceAgentIPMI = "fence_ipmilan"
	farPodName     = "far-pod"
	testPodName    = "far-pod-test-1"

	// intervals
	timeoutPreRemediation  = "5s" // this timeout is used for the other steps that occur before remediation is completed
	timeoutPostRemediation = "2s" // this timeout is used for the other steps that occur after remediation is completed
	pollInterval           = "200ms"

	// eventSteps
	eventExist    = "Verifying that event %s was created from"
	eventNotExist = "Verifying that event %s was not created from"
)

var (
	faPodLabels = map[string]string{"app.kubernetes.io/name": "fence-agents-remediation-operator"}
	log         = ctrl.Log.WithName("controllers-unit-test")
)

var _ = Describe("FAR Controller", func() {
	var (
		node                     *corev1.Node
		underTestFAR             = &v1alpha1.FenceAgentsRemediation{}
		nodeSecret, sharedSecret = &corev1.Secret{}, &corev1.Secret{}
	)

	noActionShareParam := map[v1alpha1.ParameterName]string{
		"--username": "admin",
		"--password": "password",
		"--ip":       "192.168.111.1",
		"--lanplus":  "",
	}
	//Using a predefined 'map' for the 'off' action to prioritize test readability and avoid deep copy code in each test.
	offActionShareParam := map[v1alpha1.ParameterName]string{
		"--username": "admin",
		"--password": "password",
		"--action":   "off",
		"--ip":       "192.168.111.1",
		"--lanplus":  "",
	}
	invalidActionShareParam := map[v1alpha1.ParameterName]string{
		"--username": "admin",
		"--password": "password",
		"--action":   "cycle",
		"--ip":       "192.168.111.1",
		"--lanplus":  "",
	}
	testShareParam := map[v1alpha1.ParameterName]string{
		"--username": "admin",
		"--password": "password",
		"--action":   "reboot",
		"--ip":       "192.168.111.1",
		"--lanplus":  "",
	}
	testShareParamTwice := map[v1alpha1.ParameterName]string{
		"--username": "admin",
		"--ipport":   "600",
		"--password": "password",
		"--action":   "reboot",
		"--ip":       "192.168.111.1",
		"--lanplus":  "",
	}
	testNodeParam := map[v1alpha1.ParameterName]map[v1alpha1.NodeName]string{
		"--ipport": {
			"master-0": "6230",
			"master-1": "6231",
			"master-2": "6232",
			"worker-0": "6233",
			"worker-1": "6234",
			"worker-2": "6235",
		},
	}
	BeforeEach(func() {
		storedCommand = storedCommand[:0]
	})

	Context("Reconcile with ResourceDeletion strategy", func() {
		nodeSecretName := fmt.Sprintf("fence-agents-credentials-node-%s", workerNode)
		sharedSecretName := "fence-agents-credentials-shared"
		farRemediationTaint := utils.CreateRemediationTaint()
		conditionStatusPointer := func(status metav1.ConditionStatus) *metav1.ConditionStatus { return &status }
		underTestFAR = getFenceAgentsRemediation(workerNode, fenceAgentIPMI, testShareParam, testNodeParam, v1alpha1.ResourceDeletionRemediationStrategy)
		BeforeEach(func() {
			// Create two pods and at the end clean them up with DeferCleanup
			testPod := createRunningPod("far-test-1", testPodName, workerNode)
			DeferCleanup(cleanupTestedResources, testPod)

			farPod := createRunningPod("far-manager-test", farPodName, "")
			DeferCleanup(k8sClient.Delete, context.Background(), farPod)

			nodeSecret = generateSecret(nodeSecretName, map[string][]byte{
				"--pass":  []byte("abc"),
				"--pass2": []byte("abc2"),
			})
			sharedSecret = generateSecret(sharedSecretName, map[string][]byte{})
		})

		JustBeforeEach(func() {
			// Create node, and FAR CR, and at the end clean them up with DeferCleanup
			Expect(k8sClient.Create(context.Background(), node)).To(Succeed())
			DeferCleanup(func() {
				if err := k8sClient.Delete(context.Background(), node); err != nil && !apierrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred())
				}
			})

			Expect(k8sClient.Create(context.Background(), nodeSecret)).To(Succeed())
			DeferCleanup(k8sClient.Delete, context.Background(), nodeSecret)

			if sharedSecret != nil {
				Expect(k8sClient.Create(context.Background(), sharedSecret)).To(Succeed())
				DeferCleanup(k8sClient.Delete, context.Background(), sharedSecret)
			}

			Expect(k8sClient.Create(context.Background(), underTestFAR)).To(Succeed())
			DeferCleanup(func() {
				Expect(cleanupFar(context.Background(), underTestFAR)).To(Succeed())
				deleteErr := k8sClient.Get(ctx, client.ObjectKeyFromObject(underTestFAR), &v1alpha1.FenceAgentsRemediation{})
				if !apierrors.IsNotFound(deleteErr) {
					verifyEvent(corev1.EventTypeNormal, utils.EventReasonRemoveFinalizer, utils.EventMessageRemoveFinalizer)
				}
				clearEvents()
			})

			// Sleep for a second to ensure dummy reconciliation has begun running before the unit tests
			time.Sleep(1 * time.Second)
		})
		Context("Verify correct params", func() {
			BeforeEach(func() {
				node = utils.GetNode("", workerNode)
			})
			When("A param is defined both in shared Secret and in node Secret", func() {
				BeforeEach(func() {
					sharedSecret = generateSecret(sharedSecretName, map[string][]byte{
						"--mock-secure-param-a": []byte("mock-top-secret-shared-value"),
						"--mock-secure-param-b": []byte("mock-top-secret-value-b"),
					})
					nodeSecret = generateSecret(nodeSecretName, map[string][]byte{
						"--mock-secure-param-a": []byte("mock-top-secret-node-value"),
						"--mock-secure-param-c": []byte("mock-top-secret-value-c"),
					})
					underTestFAR = getFenceAgentsRemediation(workerNode, fenceAgentIPMI, testShareParam, testNodeParam, v1alpha1.ResourceDeletionRemediationStrategy)

				})
				It("Node Secret param should override shared Secret param", func() {
					Eventually(func(g Gomega) {
						g.Expect(storedCommand).To(ConsistOf([]string{
							"fence_ipmilan",
							"--lanplus",
							"--password=password",
							"--username=admin",
							"--action=reboot",
							"--ip=192.168.111.1",
							"--mock-secure-param-a=mock-top-secret-node-value",
							"--mock-secure-param-b=mock-top-secret-value-b",
							"--mock-secure-param-c=mock-top-secret-value-c",
							"--ipport=6233"}))
					}, timeoutPreRemediation, pollInterval).Should(Succeed())
				})
			})
			When("A param is defined both as secure (Secret) and non-secure", func() {
				When("A param is defined both in sharedSecret and in shared params", func() {
					BeforeEach(func() {
						dupParamKey := "--mockparam"
						testShareParam[v1alpha1.ParameterName(dupParamKey)] = "mockValue"
						DeferCleanup(func() { delete(testShareParam, v1alpha1.ParameterName(dupParamKey)) })

						sharedSecret = generateSecret(sharedSecretName, map[string][]byte{
							dupParamKey: []byte("mockValue"),
						})

						underTestFAR = getFenceAgentsRemediation(workerNode, fenceAgentIPMI, testShareParam, testNodeParam, v1alpha1.ResourceDeletionRemediationStrategy)
					})
					It("A validation error would prevent execution of fence agent command", func() {
						// No remediation should be executed due to an error
						Consistently(func(g Gomega) {
							g.Expect(storedCommand).To(BeEmpty())
						}, timeoutPreRemediation, pollInterval).Should(Succeed())
						// Taint is added before execution of fence agent takes place
						verifyEvent(corev1.EventTypeNormal, utils.EventReasonAddRemediationTaint, utils.EventMessageAddRemediationTaint)
						// Actual execution does not happen because of the validation error
						verifyNoEvent(corev1.EventTypeNormal, utils.EventReasonFenceAgentExecuted, utils.EventMessageFenceAgentExecuted)

					})
				})
				When("A param is defined both in sharedSecret and in node params", func() {
					BeforeEach(func() {
						dupParamKey := "--mockparam"
						testNodeParam[v1alpha1.ParameterName(dupParamKey)] = map[v1alpha1.NodeName]string{workerNode: "mockValue"}
						DeferCleanup(func() { delete(testNodeParam, v1alpha1.ParameterName(dupParamKey)) })

						sharedSecret = generateSecret(sharedSecretName, map[string][]byte{
							dupParamKey: []byte("mockValue"),
						})
						underTestFAR = getFenceAgentsRemediation(workerNode, fenceAgentIPMI, testShareParam, testNodeParam, v1alpha1.ResourceDeletionRemediationStrategy)
					})
					It("A validation error would prevent execution of fence agent command", func() {
						// No remediation should be executed due to an error
						Consistently(func(g Gomega) {
							g.Expect(storedCommand).To(BeEmpty())
						}, timeoutPreRemediation, pollInterval).Should(Succeed())
						// Taint is added before execution of fence agent takes place
						verifyEvent(corev1.EventTypeNormal, utils.EventReasonAddRemediationTaint, utils.EventMessageAddRemediationTaint)
						// Actual execution does not happen because of the validation error
						verifyNoEvent(corev1.EventTypeNormal, utils.EventReasonFenceAgentExecuted, utils.EventMessageFenceAgentExecuted)

					})
				})
				When("A param is defined both in node Secret and in shared params", func() {
					BeforeEach(func() {
						dupParamKey := "--mockparam"
						testShareParam[v1alpha1.ParameterName(dupParamKey)] = "mockValue"
						DeferCleanup(func() { delete(testShareParam, v1alpha1.ParameterName(dupParamKey)) })

						nodeSecret = generateSecret(nodeSecretName, map[string][]byte{
							dupParamKey: []byte("mockValue"),
						})

						underTestFAR = getFenceAgentsRemediation(workerNode, fenceAgentIPMI, testShareParam, testNodeParam, v1alpha1.ResourceDeletionRemediationStrategy)
					})
					It("A validation error would prevent execution of fence agent command", func() {
						// No remediation should be executed due to an error
						Consistently(func(g Gomega) {
							g.Expect(storedCommand).To(BeEmpty())
						}, timeoutPreRemediation, pollInterval).Should(Succeed())
						// Taint is added before execution of fence agent takes place
						verifyEvent(corev1.EventTypeNormal, utils.EventReasonAddRemediationTaint, utils.EventMessageAddRemediationTaint)
						// Actual execution does not happen because of the validation error
						verifyNoEvent(corev1.EventTypeNormal, utils.EventReasonFenceAgentExecuted, utils.EventMessageFenceAgentExecuted)

					})
				})
				When("A param is defined both in node Secret and in node params", func() {
					BeforeEach(func() {
						dupParamKey := "--mockparam"
						testNodeParam[v1alpha1.ParameterName(dupParamKey)] = map[v1alpha1.NodeName]string{workerNode: "mockValue"}
						DeferCleanup(func() { delete(testNodeParam, v1alpha1.ParameterName(dupParamKey)) })

						nodeSecret = generateSecret(nodeSecretName, map[string][]byte{
							dupParamKey: []byte("mockValue"),
						})

						underTestFAR = getFenceAgentsRemediation(workerNode, fenceAgentIPMI, testShareParam, testNodeParam, v1alpha1.ResourceDeletionRemediationStrategy)
					})
					It("A validation error would prevent execution of fence agent command", func() {
						// No remediation should be executed due to an error
						Consistently(func(g Gomega) {
							g.Expect(storedCommand).To(BeEmpty())
						}, timeoutPreRemediation, pollInterval).Should(Succeed())
						// Taint is added before execution of fence agent takes place
						verifyEvent(corev1.EventTypeNormal, utils.EventReasonAddRemediationTaint, utils.EventMessageAddRemediationTaint)
						// Actual execution does not happen because of the validation error
						verifyNoEvent(corev1.EventTypeNormal, utils.EventReasonFenceAgentExecuted, utils.EventMessageFenceAgentExecuted)

					})
				})
			})
			When("FAR CR misses the action parameter", func() {
				BeforeEach(func() {
					underTestFAR = getFenceAgentsRemediation(workerNode, fenceAgentIPMI, noActionShareParam, testNodeParam, v1alpha1.ResourceDeletionRemediationStrategy)
				})
				It("should succeed and add the action parameter with value reboot", func() {
					Eventually(func(g Gomega) {
						g.Expect(storedCommand).To(ConsistOf([]string{
							"fence_ipmilan",
							"--lanplus",
							"--password=password",
							"--username=admin",
							"--action=reboot",
							"--ip=192.168.111.1",
							"--pass2=abc2",
							"--pass=abc",
							"--ipport=6233"}))
					}, timeoutPreRemediation, pollInterval).Should(Succeed())
				})
			})
			When("The 'action' parameter is set to 'off'", func() {
				BeforeEach(func() {
					underTestFAR = getFenceAgentsRemediation(workerNode, fenceAgentIPMI, offActionShareParam, testNodeParam, v1alpha1.ResourceDeletionRemediationStrategy)
				})
				It("should succeed and include the 'action' parameter with value 'off'", func() {
					// Verify that the 'action' parameter is correctly set to 'off'
					Eventually(func(g Gomega) {
						g.Expect(storedCommand).To(ConsistOf([]string{
							"fence_ipmilan",
							"--lanplus",
							"--password=password",
							"--username=admin",
							"--action=off",
							"--ip=192.168.111.1",
							"--pass2=abc2",
							"--pass=abc",
							"--ipport=6233"}))
					}, timeoutPreRemediation, pollInterval).Should(Succeed())
				})
			})
			When("The 'action' parameter is set to 'reboot'", func() {
				BeforeEach(func() {
					// Use the existing testShareParam which has 'action' set to 'reboot'
					underTestFAR = getFenceAgentsRemediation(workerNode, fenceAgentIPMI, testShareParam, testNodeParam, v1alpha1.ResourceDeletionRemediationStrategy)
				})
				It("should succeed and include the 'action' parameter with value 'reboot'", func() {
					// Verify that the 'action' parameter is correctly set to 'reboot'
					Eventually(func(g Gomega) {
						g.Expect(storedCommand).To(ConsistOf([]string{
							"fence_ipmilan",
							"--lanplus",
							"--password=password",
							"--username=admin",
							"--action=reboot",
							"--ip=192.168.111.1",
							"--pass2=abc2",
							"--pass=abc",
							"--ipport=6233"}))
					}, timeoutPreRemediation, pollInterval).Should(Succeed())
				})
			})
			When("Param defined both in Node and shared params", func() {
				BeforeEach(func() {
					underTestFAR = getFenceAgentsRemediation(workerNode, fenceAgentIPMI, testShareParamTwice, testNodeParam, v1alpha1.ResourceDeletionRemediationStrategy)

				})
				It("Node param should be used with ipport `6233`", func() {
					Eventually(func(g Gomega) {
						g.Expect(storedCommand).To(ConsistOf([]string{
							"fence_ipmilan",
							"--lanplus",
							"--password=password",
							"--username=admin",
							"--action=reboot",
							"--ip=192.168.111.1",
							"--pass2=abc2",
							"--pass=abc",
							"--ipport=6233"}))
					}, timeoutPreRemediation, pollInterval).Should(Succeed())
				})

			})
			When("shared secret contains NodeName template", func() {
				BeforeEach(func() {
					sharedSecret = generateSecret(sharedSecretName, map[string][]byte{
						"--systems-uri": []byte("/redfish/v1/Systems/{{.NodeName}}"),
						"--hostname":    []byte("{{.NodeName}}.example.com"),
						"--port":        []byte("623"), // No template, should be fine
					})
					underTestFAR = getFenceAgentsRemediation(workerNode, fenceAgentIPMI, testShareParam, testNodeParam, v1alpha1.ResourceDeletionRemediationStrategy)
				})
				It("should process NodeName template correctly", func() {
					Eventually(func(g Gomega) {
						g.Expect(storedCommand).To(ConsistOf([]string{
							"fence_ipmilan",
							"--lanplus",
							"--password=password",
							"--username=admin",
							"--action=reboot",
							"--ip=192.168.111.1",
							"--systems-uri=/redfish/v1/Systems/worker-0",
							"--hostname=worker-0.example.com",
							"--port=623",
							"--pass=abc",
							"--pass2=abc2",
							"--ipport=6233"}))
					}, timeoutPreRemediation, pollInterval).Should(Succeed())
				})
			})

			When("shared secret contains invalid NodeName template", func() {
				BeforeEach(func() {
					sharedSecret = generateSecret(sharedSecretName, map[string][]byte{
						"--systems-uri": []byte("/redfish/v1/Systems/{{.NodeName"), // Missing closing brace
						"--hostname":    []byte("{{.InvalidField}}"),               // Invalid field
					})
					underTestFAR = getFenceAgentsRemediation(workerNode, fenceAgentIPMI, testShareParam, testNodeParam, v1alpha1.ResourceDeletionRemediationStrategy)
				})
				It("should prevent fence agent execution due to template processing error", func() {
					// No remediation should be executed due to template processing error
					Consistently(func(g Gomega) {
						g.Expect(storedCommand).To(BeEmpty())
					}, timeoutPreRemediation, pollInterval).Should(Succeed())
					// Taint is added before template processing takes place
					verifyEvent(corev1.EventTypeNormal, utils.EventReasonAddRemediationTaint, utils.EventMessageAddRemediationTaint)
					// Actual execution does not happen because of the template processing error
					verifyNoEvent(corev1.EventTypeNormal, utils.EventReasonFenceAgentExecuted, utils.EventMessageFenceAgentExecuted)
				})
			})

		})
		When("creating valid FAR CR", func() {

			testSuccessfulRemediation := func() {
				Eventually(func(g Gomega) {
					g.Expect(storedCommand).To(ConsistOf([]string{
						"fence_ipmilan",
						"--pass=abc",
						"--pass2=abc2",
						"--lanplus",
						"--password=password",
						"--username=admin",
						"--action=reboot",
						"--ip=192.168.111.1",
						"--ipport=6233"}))
				}, timeoutPreRemediation, pollInterval).Should(Succeed())

				underTestFAR = verifyPreRemediationSucceed(underTestFAR, defaultNamespace, &farRemediationTaint)

				By("Not having any test pod")
				verifyPodDeleted(testPodName)

				By("Verifying correct conditions for successful remediation")
				verifyRemediationConditions(
					underTestFAR,
					conditionStatusPointer(metav1.ConditionFalse), // ProcessingTypeStatus
					conditionStatusPointer(metav1.ConditionTrue),  // FenceAgentActionSucceededTypeStatus
					conditionStatusPointer(metav1.ConditionTrue))  // SucceededTypeStatus
				verifyEvent(corev1.EventTypeNormal, utils.EventReasonFenceAgentSucceeded, utils.EventMessageFenceAgentSucceeded)
				verifyEvent(corev1.EventTypeNormal, utils.EventReasonNodeRemediationCompleted, utils.EventMessageNodeRemediationCompleted)
			}
			BeforeEach(func() {
				node = utils.GetNode("", workerNode)
				underTestFAR = getFenceAgentsRemediation(workerNode, fenceAgentIPMI, testShareParam, testNodeParam, v1alpha1.ResourceDeletionRemediationStrategy)
			})
			When("node name is stored in remediation name", func() {
				It("should have finalizer and taint, while the tested pod will be deleted", testSuccessfulRemediation)
			})
			//remediation is created from escalation remediation supporting same kind template
			When("node name is stored in remediation's annotation", func() {
				BeforeEach(func() {
					underTestFAR.Name = fmt.Sprintf("%s-%s", workerNode, "pseudo-random-test-sufix")
					underTestFAR.Annotations = map[string]string{"remediation.medik8s.io/node-name": workerNode}

					// test shared secret name default value workaround: existing secret should be used even when name is nil
					underTestFAR.Spec.SharedSecretName = nil
				})
				It("should have finalizer and taint, while the tested pod will be deleted", testSuccessfulRemediation)
			})
		})
		When("creating invalid FAR CR Name", func() {
			BeforeEach(func() {
				node = utils.GetNode("", workerNode)
				underTestFAR = getFenceAgentsRemediation(dummyNode, fenceAgentIPMI, testShareParam, testNodeParam, v1alpha1.ResourceDeletionRemediationStrategy)
			})

			It("should not have a finalizer nor taint, while the tested pod will remain", func() {
				By("Not finding a matching node to FAR CR's name")
				Expect(k8sClient.Get(context.Background(), client.ObjectKey{Name: underTestFAR.Name}, node)).To(Not(Succeed()))

				By("Not having finalizer")
				Consistently(func(g Gomega) bool {
					g.Expect(k8sClient.Get(context.Background(), client.ObjectKey{Name: underTestFAR.Name, Namespace: defaultNamespace}, underTestFAR)).To(Succeed())
					return controllerutil.ContainsFinalizer(underTestFAR, v1alpha1.FARFinalizer)
				}, timeoutPreRemediation, pollInterval).Should(BeFalse(), "finalizer shouldn't be added")
				verifyEvent(corev1.EventTypeWarning, utils.EventReasonCrNodeNotFound, utils.EventMessageCrNodeNotFound)
				verifyNoEvent(corev1.EventTypeNormal, utils.EventReasonRemediationStarted, utils.EventMessageRemediationStarted)

				// If finalizer is missing, then a taint shouldn't exist
				By("Not having remediation taint")
				Expect(utils.TaintExists(node.Spec.Taints, &farRemediationTaint)).To(BeFalse())
				verifyNoEvent(corev1.EventTypeNormal, utils.EventReasonAddRemediationTaint, utils.EventMessageAddRemediationTaint)

				By("Still having one test pod")
				verifyPodExists(testPodName)

				By("Verifying correct conditions for unsuccessful remediation")
				verifyRemediationConditions(
					underTestFAR,
					conditionStatusPointer(metav1.ConditionFalse), // ProcessingTypeStatus
					conditionStatusPointer(metav1.ConditionFalse), // FenceAgentActionSucceededTypeStatus
					conditionStatusPointer(metav1.ConditionFalse)) // SucceededTypeStatus
				verifyNoEvent(corev1.EventTypeNormal, utils.EventReasonFenceAgentSucceeded, utils.EventMessageFenceAgentSucceeded)
				verifyNoEvent(corev1.EventTypeNormal, utils.EventReasonNodeRemediationCompleted, utils.EventReasonNodeRemediationCompleted)
			})
		})
		When("FAR CR is NHC-timed-out and the node is deleted (e.g. by MDR)", func() {
			BeforeEach(func() {
				node = utils.GetNode("", workerNode)
				underTestFAR = getFenceAgentsRemediation(workerNode, fenceAgentIPMI, testShareParam, testNodeParam, v1alpha1.ResourceDeletionRemediationStrategy)
			})

			It("should remove the finalizer and allow the CR deletion to complete", func() {
				By("Waiting for the finalizer to be added (remediation has started)")
				underTestFAR = verifyPreRemediationSucceed(underTestFAR, defaultNamespace, &farRemediationTaint)

				By("Simulating NHC timeout: adding the NHC timed-out annotation")
				Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(underTestFAR), underTestFAR)).To(Succeed())
				if underTestFAR.Annotations == nil {
					underTestFAR.Annotations = map[string]string{}
				}
				underTestFAR.Annotations[commonAnnotations.NhcTimedOut] = time.Now().Format(time.RFC3339)
				Expect(k8sClient.Update(context.Background(), underTestFAR)).To(Succeed())

				By("Simulating MDR deleting the node")
				Expect(k8sClient.Delete(context.Background(), node)).To(Succeed())

				By("Simulating NHC deleting the FAR CR")
				Expect(k8sClient.Delete(context.Background(), underTestFAR)).To(Succeed())

				By("Expecting the finalizer to be removed and the CR deletion to complete")
				Eventually(func() bool {
					err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(underTestFAR), &v1alpha1.FenceAgentsRemediation{})
					return apierrors.IsNotFound(err)
				}, timeoutPreRemediation, pollInterval).Should(BeTrue(), "FAR CR should have been deleted — finalizer must have been removed")
			})
		})

		When("FAR CR is deleted and the node is already gone (no NHC timeout)", func() {
			BeforeEach(func() {
				node = utils.GetNode("", workerNode)
				underTestFAR = getFenceAgentsRemediation(workerNode, fenceAgentIPMI, testShareParam, testNodeParam, v1alpha1.ResourceDeletionRemediationStrategy)
			})

			It("should remove the finalizer and allow the CR deletion to complete", func() {
				By("Waiting for the finalizer to be added (remediation has started)")
				underTestFAR = verifyPreRemediationSucceed(underTestFAR, defaultNamespace, &farRemediationTaint)

				By("Simulating node deletion (e.g., cloud provider removed it, manual deletion)")
				Expect(k8sClient.Delete(context.Background(), node)).To(Succeed())

				By("User/system deletes the FAR CR (no NHC timeout annotation)")
				Expect(k8sClient.Delete(context.Background(), underTestFAR)).To(Succeed())

				By("Expecting the finalizer to be removed and the CR deletion to complete")
				Eventually(func() bool {
					err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(underTestFAR), &v1alpha1.FenceAgentsRemediation{})
					return apierrors.IsNotFound(err)
				}, timeoutPreRemediation, pollInterval).Should(BeTrue(), "FAR CR should have been deleted even without NHC timeout — finalizer must have been removed")
			})
		})

		When("create FAR CR with invalid Action parameter", func() {
			BeforeEach(func() {
				node = utils.GetNode("", workerNode)
				underTestFAR = getFenceAgentsRemediation(workerNode, fenceAgentIPMI, invalidActionShareParam, testNodeParam, v1alpha1.ResourceDeletionRemediationStrategy)
			})
			It("should trigger ParamInvalid Event", func() {
				verifyEvent(corev1.EventTypeWarning, utils.EventReasonCrUnsupportedAction, utils.EventMessageCrUnsupportedAction)
			})
		})
		Context("Fence agent failures", func() {
			BeforeEach(func() {
				plogs.Clear()
				node = utils.GetNode("", workerNode)
				underTestFAR = getFenceAgentsRemediation(workerNode, fenceAgentIPMI, testShareParam, testNodeParam, v1alpha1.ResourceDeletionRemediationStrategy)
			})

			When("CR is deleted in between fence agent retries", func() {
				BeforeEach(func() {
					// Fail controlledRun to simulate a case where CR deletion occurs in between consecutive fence agent
					// command calls.
					mockError = errors.New("mock error")
					DeferCleanup(func() { mockError = nil })

					underTestFAR.Spec.RetryCount = 100
					underTestFAR.Spec.RetryInterval = metav1.Duration{Duration: 1 * time.Second}
				})

				It("should exit immediately without trying to update the status conditions", func() {
					underTestFAR = verifyPreRemediationSucceed(underTestFAR, defaultNamespace, &farRemediationTaint)

					By("Wait some retries")
					Eventually(func() int {
						return plogs.CountOccurences(cli.FenceAgentFailedCommandMessage)
					}, "10s", "1s").Should(BeNumerically(">", 3))

					By("Deleting FAR CR")
					Expect(k8sClient.Delete(context.Background(), underTestFAR)).To(Succeed())

					By("Verifying goroutine stopped without trying to update the conditions")
					Eventually(func() bool {
						return plogs.Contains(cli.FenceAgentContextCanceledMessage)
					}).Should(BeTrue())
					verifyNoEvent(corev1.EventTypeNormal, utils.EventReasonFenceAgentSucceeded, utils.EventMessageFenceAgentSucceeded)
				})
			})

			When("CR is deleted during fence agent execution", func() {
				BeforeEach(func() {
					// Fail controlledRun to simulate a case where CR deletion occurs during a fence agent
					// command call.
					forcedDelay = 10 * time.Second
					DeferCleanup(func() { forcedDelay = 0 })

					underTestFAR.Spec.RetryCount = 100
					underTestFAR.Spec.RetryInterval = metav1.Duration{Duration: 1 * time.Second}
				})

				It("should exit immediately without trying to update the status conditions", func() {
					underTestFAR = verifyPreRemediationSucceed(underTestFAR, defaultNamespace, &farRemediationTaint)

					By("Deleting FAR CR")
					Expect(k8sClient.Delete(context.Background(), underTestFAR)).To(Succeed())

					By("Verifying goroutine stopped without trying to update the conditions")
					Eventually(func() bool {
						return plogs.Contains(cli.FenceAgentContextCanceledMessage)
					}).Should(BeTrue())
					verifyNoEvent(corev1.EventTypeNormal, utils.EventReasonFenceAgentSucceeded, utils.EventMessageFenceAgentSucceeded)
				})
			})

			When("Fence Agent command fails", func() {
				BeforeEach(func() {
					mockError = errors.New("mock error")
					DeferCleanup(func() { mockError = nil })

					underTestFAR.Spec.RetryCount = 3
					underTestFAR.Spec.RetryInterval = metav1.Duration{Duration: 1 * time.Millisecond}
				})

				It("should retry the fence agent command as configured and update the status accordingly", func() {
					underTestFAR = verifyPreRemediationSucceed(underTestFAR, defaultNamespace, &farRemediationTaint)

					By("Still having one test pod")
					verifyPodExists(testPodName)

					By("Reading the expected number of retries")
					Eventually(func() int {
						return plogs.CountOccurences(cli.FenceAgentFailedCommandMessage)
					}).Should(Equal(3))

					By("Verifying correct conditions for un-successful remediation")
					verifyRemediationConditions(
						underTestFAR,
						conditionStatusPointer(metav1.ConditionFalse), // ProcessingTypeStatus
						conditionStatusPointer(metav1.ConditionFalse), // FenceAgentActionSucceededTypeStatus
						conditionStatusPointer(metav1.ConditionFalse)) // SucceededTypeStatus
					verifyNoEvent(corev1.EventTypeNormal, utils.EventReasonFenceAgentSucceeded, utils.EventMessageFenceAgentSucceeded)
				})
			})

			When("Fence Agent command times out", func() {
				BeforeEach(func() {
					forcedDelay = 10 * time.Second
					DeferCleanup(func() { forcedDelay = 0 })

					underTestFAR.Spec.Timeout = metav1.Duration{Duration: 2 * time.Second}
				})

				It("should stop Fence Agent execution and update the status accordingly", func() {
					underTestFAR = verifyPreRemediationSucceed(underTestFAR, defaultNamespace, &farRemediationTaint)

					By("Still having one test pod")
					verifyPodExists(testPodName)

					By("Context timeout occurred")
					Eventually(func() bool {
						return plogs.Contains(cli.FenceAgentContextTimedOutMessage)
					}).Should(BeTrue(), "fence agent should have timed out")

					By("Verifying correct conditions for un-successful remediation")
					verifyRemediationConditions(
						underTestFAR,
						conditionStatusPointer(metav1.ConditionFalse), // ProcessingTypeStatus
						conditionStatusPointer(metav1.ConditionFalse), // FenceAgentActionSucceededTypeStatus
						conditionStatusPointer(metav1.ConditionFalse)) // SucceededTypeStatus
					verifyNoEvent(corev1.EventTypeNormal, utils.EventReasonFenceAgentSucceeded, utils.EventMessageFenceAgentSucceeded)
				})
			})
		})
	})

	Context("Reconcile with OutOfServiceTaint strategy", func() {
		farRemediationTaint := utils.CreateRemediationTaint()
		outOfServiceTaint := utils.CreateOutOfServiceTaint()
		conditionStatusPointer := func(status metav1.ConditionStatus) *metav1.ConditionStatus { return &status }

		BeforeEach(func() {
			farPod := createRunningPod("far-manager-test", farPodName, "")
			DeferCleanup(k8sClient.Delete, context.Background(), farPod)
		})

		JustBeforeEach(func() {
			// Create node, and FAR CR, and at the end clean them up with DeferCleanup
			Expect(k8sClient.Create(context.Background(), node)).To(Succeed())
			DeferCleanup(k8sClient.Delete, context.Background(), node)

			Expect(k8sClient.Create(context.Background(), underTestFAR)).To(Succeed())
			DeferCleanup(func() {
				Expect(cleanupFar(context.Background(), underTestFAR)).To(Succeed())
				deleteErr := k8sClient.Get(ctx, client.ObjectKeyFromObject(underTestFAR), &v1alpha1.FenceAgentsRemediation{})
				if !apierrors.IsNotFound(deleteErr) {
					verifyEvent(corev1.EventTypeNormal, utils.EventReasonRemoveFinalizer, utils.EventMessageRemoveFinalizer)
				}
				clearEvents()
			})

			// Sleep for a second to ensure dummy reconciliation has begun running before the unit tests
			time.Sleep(1 * time.Second)
		})

		When("creating valid FAR CR", func() {
			BeforeEach(func() {
				node = utils.GetNode("", workerNode)
				underTestFAR = getFenceAgentsRemediation(workerNode, fenceAgentIPMI, testShareParam, testNodeParam, v1alpha1.OutOfServiceTaintRemediationStrategy)
				underTestFAR.Spec.NodeSecretNames = nil
				underTestFAR.Spec.SharedSecretName = nil
			})

			It("should have finalizer, both remediation taint and out-of-service taint, and at the end they will be deleted", func() {
				Eventually(func(g Gomega) {
					g.Expect(storedCommand).To(ConsistOf([]string{
						"fence_ipmilan",
						"--lanplus",
						"--password=password",
						"--username=admin",
						"--action=reboot",
						"--ip=192.168.111.1",
						"--ipport=6233"}))
				}, timeoutPreRemediation, pollInterval).Should(Succeed())

				underTestFAR = verifyPreRemediationSucceed(underTestFAR, defaultNamespace, &farRemediationTaint)

				By("Searching for out-of-service taint")
				Eventually(func(g Gomega) {
					node := &corev1.Node{}
					g.Expect(k8sClient.Get(context.Background(), client.ObjectKey{Name: workerNode}, node)).To(Succeed())
					g.Expect(utils.TaintExists(node.Spec.Taints, &outOfServiceTaint)).To(BeTrue(), "out-of-service taint should exist")
				}, timeoutPostRemediation, pollInterval).Should(Succeed())

				By("Verifying correct conditions for successful remediation")
				verifyRemediationConditions(
					underTestFAR,
					conditionStatusPointer(metav1.ConditionFalse), // ProcessingTypeStatus
					conditionStatusPointer(metav1.ConditionTrue),  // FenceAgentActionSucceededTypeStatus
					conditionStatusPointer(metav1.ConditionTrue))  // SucceededTypeStatus
				verifyEvent(corev1.EventTypeNormal, utils.EventReasonAddOutOfServiceTaint, utils.EventMessageAddOutOfServiceTaint)
				verifyEvent(corev1.EventTypeNormal, utils.EventReasonNodeRemediationCompleted, utils.EventMessageNodeRemediationCompleted)

				By("Deleting FAR CR")
				Expect(k8sClient.Delete(context.Background(), underTestFAR)).To(Succeed())

				Eventually(func(g Gomega) {
					node := &corev1.Node{}
					g.Expect(k8sClient.Get(context.Background(), client.ObjectKey{Name: workerNode}, node)).To(Succeed())
					g.Expect(utils.TaintExists(node.Spec.Taints, &outOfServiceTaint)).To(BeFalse(), "out-of-service taint should be removed")
				}, timeoutPostRemediation, pollInterval).Should(Succeed())
				verifyEvent(corev1.EventTypeNormal, utils.EventReasonRemoveOutOfServiceTaint, utils.EventMessageRemoveOutOfServiceTaint)
			})
		})
	})
})

// getFenceAgentsRemediation assigns the input to the FenceAgentsRemediation
func getFenceAgentsRemediation(nodeName, agent string, sharedparameters map[v1alpha1.ParameterName]string, nodeparameters map[v1alpha1.ParameterName]map[v1alpha1.NodeName]string, strategy v1alpha1.RemediationStrategyType) *v1alpha1.FenceAgentsRemediation {
	sharedSecretName := "fence-agents-credentials-shared"
	return &v1alpha1.FenceAgentsRemediation{
		ObjectMeta: metav1.ObjectMeta{Name: nodeName, Namespace: defaultNamespace},
		Spec: v1alpha1.FenceAgentsRemediationSpec{
			Agent:            agent,
			SharedParameters: sharedparameters,
			NodeParameters:   nodeparameters,
			// Set the retry count to the minimum for the majority of the tests
			RetryCount:          1,
			RetryInterval:       metav1.Duration{Duration: 5 * time.Second},
			Timeout:             metav1.Duration{Duration: 60 * time.Second},
			RemediationStrategy: strategy,
			SharedSecretName:    &sharedSecretName,
			NodeSecretNames:     map[v1alpha1.NodeName]string{v1alpha1.NodeName(nodeName): fmt.Sprintf("fence-agents-credentials-node-%s", nodeName)},
		},
	}
}

// generateSecret assigns the input to the FenceAgentsRemediation
func generateSecret(secretName string, secretData map[string][]byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: defaultNamespace},
		Data:       secretData,
		Type:       corev1.SecretType("Opaque"),
	}
}

// buildPod builds a dummy pod
func buildPod(containerName, podName, nodeName string) *corev1.Pod {
	pod := &corev1.Pod{}
	pod.Name = podName
	if podName == farPodName {
		// only when we build FAR pod then we add its label
		pod.Labels = faPodLabels
	} else {
		// testedPod should reside in unhealthy node
		pod.Spec.NodeName = nodeName
	}
	pod.Namespace = defaultNamespace
	container := corev1.Container{
		Name:  containerName,
		Image: "foo",
	}
	pod.Spec.Containers = []corev1.Container{container}
	return pod
}

// createRunningPod builds new pod format, create it, and set its status as running
func createRunningPod(containerName, podName, nodeName string) *corev1.Pod {
	pod := buildPod(containerName, podName, nodeName)
	Expect(k8sClient.Create(context.Background(), pod)).To(Succeed())
	pod.Status.Phase = corev1.PodRunning
	Expect(k8sClient.Status().Update(context.Background(), pod)).To(Succeed())
	return pod
}

// cleanupTestedResources fetches all the resources that we have crated for the test
// and if they are still exist at the end of the test, then we clean them up for next test
func cleanupTestedResources(pod *corev1.Pod) {
	podTest := &corev1.Pod{}
	key := client.ObjectKeyFromObject(pod)
	if err := k8sClient.Get(context.Background(), key, podTest); err == nil {
		log.Info("Cleanup: clean pod", "pod name", podTest.Name)

		// Delete the resource immediately
		var force client.GracePeriodSeconds = 0
		Expect(k8sClient.Delete(context.Background(), podTest, force)).To(Succeed())
	}
}

// verifyPodDeleted verifies whether the pod no longer exists for successful FAR CR
func verifyPodDeleted(podName string) {
	pod := &corev1.Pod{}
	podKey := client.ObjectKey{
		Namespace: defaultNamespace,
		Name:      podName,
	}
	EventuallyWithOffset(1, func() bool {
		err := k8sClient.Get(context.Background(), podKey, pod)
		return apierrors.IsNotFound(err)
	}, timeoutPostRemediation, pollInterval).Should(BeTrue())
	log.Info("Pod not longer exists", "pod", podName)
}

// verifyPodExists verifies whether the pod exists and was not deleted
func verifyPodExists(podName string) {
	pod := &corev1.Pod{}
	podKey := client.ObjectKey{
		Namespace: defaultNamespace,
		Name:      podName,
	}
	ConsistentlyWithOffset(1, func() bool {
		err := k8sClient.Get(context.Background(), podKey, pod)
		return apierrors.IsNotFound(err)
	}, timeoutPostRemediation, pollInterval).Should(BeFalse())
	log.Info("Pod exists", "pod", podName)
}

// verifyStatusCondition checks if the status condition is not set, and if it is set then it has an expected value
func verifyStatusCondition(far *v1alpha1.FenceAgentsRemediation, conditionType string, conditionStatus *metav1.ConditionStatus) {
	Eventually(func(g Gomega) {
		//g.Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(far), far)).To(Succeed())
		condition := meta.FindStatusCondition(far.Status.Conditions, conditionType)
		if conditionStatus == nil {
			g.Expect(condition).To(BeNil(), "expected condition %v to not be set", conditionType)
		} else {
			g.Expect(condition).ToNot(BeNil(), "expected condition %v to be set", conditionType)
			g.Expect(condition.Status).To(Equal(*conditionStatus), "expected condition %v to have status %v", conditionType, *conditionStatus)
		}
	}, timeoutPostRemediation, pollInterval).Should(Succeed())
}

// verifyPreRemediationSucceed checks if the remediation CR already has a finazliaer and a remediation taint
func verifyPreRemediationSucceed(underTestFAR *v1alpha1.FenceAgentsRemediation, namespace string, taint *corev1.Taint) *v1alpha1.FenceAgentsRemediation {
	By("Searching for finalizer ")
	Expect(k8sClient.Get(context.Background(), client.ObjectKey{Name: underTestFAR.GetName(), Namespace: namespace}, underTestFAR)).To(Succeed())
	Expect(controllerutil.ContainsFinalizer(underTestFAR, v1alpha1.FARFinalizer)).To(BeTrue())
	verifyEvent(corev1.EventTypeNormal, utils.EventReasonRemediationStarted, utils.EventMessageRemediationStarted)

	By("Searching for remediation taint if we have a finalizer")
	Eventually(func(g Gomega) {
		node := &corev1.Node{}
		g.Expect(k8sClient.Get(context.Background(), client.ObjectKey{Name: v1alpha1.GetNodeName(underTestFAR)}, node)).To(Succeed())
		g.Expect(utils.TaintExists(node.Spec.Taints, taint)).To(BeTrue(), "remediation taint should exist")
	}, timeoutPreRemediation, pollInterval).Should(Succeed())
	verifyEvent(corev1.EventTypeNormal, utils.EventReasonAddRemediationTaint, utils.EventMessageAddRemediationTaint)

	return underTestFAR
}

func verifyEvent(eventType, eventReason, eventMessage string) {
	eventText := fmt.Sprintf(eventExist, eventReason)
	By(eventText)
	isEventMatch := isEventOccurred(eventType, eventReason, eventMessage)
	ExpectWithOffset(1, isEventMatch).To(BeTrue())
}

func verifyNoEvent(eventType, eventReason, eventMessage string) {
	eventText := fmt.Sprintf(eventNotExist, eventReason)
	By(eventText)
	isEventMatch := isEventOccurred(eventType, eventReason, eventMessage)
	ExpectWithOffset(1, isEventMatch).To(BeFalse())
}

// isEventOccurred checks whether an event has occoured
func isEventOccurred(eventType, eventReason, eventMessage string) bool {
	expected := fmt.Sprintf("%s %s [remediation] %s", eventType, eventReason, eventMessage)
	isEventMatch := false

	unMatchedEvents := make(chan string, len(fakeRecorder.Events))
	isDone := false
	for {
		select {
		case event := <-fakeRecorder.Events:
			if isEventMatch = event == expected; isEventMatch {
				isDone = true
			} else {
				unMatchedEvents <- event
			}
		default:
			isDone = true
		}
		if isDone {
			break
		}
	}

	close(unMatchedEvents)
	for unMatchedEvent := range unMatchedEvents {
		fakeRecorder.Events <- unMatchedEvent
	}
	return isEventMatch
}

// clearEvents loop over the events channel until it is empty from events
func clearEvents() {
	for len(fakeRecorder.Events) > 0 {
		<-fakeRecorder.Events
	}
	log.Info("Cleanup: events list is empty")
}

func verifyRemediationConditions(far *v1alpha1.FenceAgentsRemediation, processingTypeConditionStatus, fenceAgentSuccededTypeConditionStatus, succededTypeConditionStatus *metav1.ConditionStatus) {
	EventuallyWithOffset(1, func(g Gomega) {
		farCR := &v1alpha1.FenceAgentsRemediation{}
		g.Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(far), farCR)).To(Succeed())
		g.Expect(farCR.Status.LastUpdateTime).ToNot(BeNil())
		verifyStatusCondition(farCR, commonConditions.ProcessingType, processingTypeConditionStatus)
		verifyStatusCondition(farCR, utils.FenceAgentActionSucceededType, fenceAgentSuccededTypeConditionStatus)
		verifyStatusCondition(farCR, commonConditions.SucceededType, succededTypeConditionStatus)
	})
}

// cleanupFar deletes the FAR CR and waits until it is deleted. The function ignores if the CR is already deleted.
func cleanupFar(ctx context.Context, far *v1alpha1.FenceAgentsRemediation) error {
	cr := &v1alpha1.FenceAgentsRemediation{}
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(far), cr); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	var force client.GracePeriodSeconds = 0
	if err := k8sClient.Delete(ctx, cr, force); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	ConsistentlyWithOffset(1, func() error {
		deleteErr := k8sClient.Get(ctx, client.ObjectKeyFromObject(far), cr)
		if apierrors.IsNotFound(deleteErr) {
			// when trying to create far CR with invalid name
			log.Info("Cleanup: Got error 404", "name", cr.Name)
			return nil
		}
		return deleteErr
	}, pollInterval, timeoutPostRemediation).Should(BeNil(), "CR should be deleted")
	return nil
}

const safetySystemUUID = "00112233-4455-4677-8899-aabbccddeeff"

type safetyHarness struct {
	client     client.Client
	reconciler *FenceAgentsRemediationReconciler
	far        *v1alpha1.FenceAgentsRemediation
	calls      atomic.Int32
}

func newSafetyHarness(t GinkgoTInterface, retries int, runnerErr error) *safetyHarness {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{corev1.AddToScheme, v1alpha1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker-a", UID: "node-a"}, Status: corev1.NodeStatus{NodeInfo: corev1.NodeSystemInfo{SystemUUID: safetySystemUUID}}}
	far := &v1alpha1.FenceAgentsRemediation{ObjectMeta: metav1.ObjectMeta{Name: node.Name, Namespace: "bmc", UID: "far-a", Finalizers: []string{v1alpha1.FARFinalizer}}, Spec: v1alpha1.FenceAgentsRemediationSpec{Agent: "fence_redfish", RetryCount: retries, RetryInterval: metav1.Duration{Duration: time.Millisecond}, Timeout: metav1.Duration{Duration: time.Second}, RemediationStrategy: v1alpha1.OutOfServiceTaintRemediationStrategy, SharedParameters: map[v1alpha1.ParameterName]string{"--action": "off"}, NodeParameters: map[v1alpha1.ParameterName]map[v1alpha1.NodeName]string{"--ip": {v1alpha1.NodeName(node.Name): "192.0.2.1"}}}}
	utils.UpdateConditions(utils.RemediationStarted, far, logr.Discard())
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(far).WithObjects(node, far).Build()
	h := &safetyHarness{client: c, far: far}
	recorder := record.NewFakeRecorder(100)
	executor := cli.NewFakeExecuter(c, func(context.Context, []string) (string, string, error) { h.calls.Add(1); return "", "", runnerErr }, recorder)
	h.reconciler = &FenceAgentsRemediationReconciler{Client: c, Scheme: scheme, Log: logr.Discard(), Recorder: recorder, Executor: executor}
	return h
}

func (h *safetyHarness) reconcile(t GinkgoTInterface) {
	t.Helper()
	if _, err := h.reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(h.far)}); err != nil {
		t.Fatal(err)
	}
}

func (h *safetyHarness) getFAR(t GinkgoTInterface) *v1alpha1.FenceAgentsRemediation {
	t.Helper()
	far := &v1alpha1.FenceAgentsRemediation{}
	if err := h.client.Get(context.Background(), client.ObjectKeyFromObject(h.far), far); err != nil {
		t.Fatal(err)
	}
	return far
}

func enableSafety(t GinkgoTInterface, h *safetyHarness) {
	t.Helper()
	fixture, config, _ := safetyFixture(t)
	far := h.getFAR(t)
	far.Spec.SafetyPolicy = fixture.Spec.SafetyPolicy
	if far.Annotations == nil {
		far.Annotations = map[string]string{}
	}
	for key, value := range fixture.Annotations {
		far.Annotations[key] = value
	}
	far.Spec.RetryInterval.Duration = time.Second
	h.reconciler.Safety = config
	h.reconciler.APIReader = h.client
	if err := h.client.Update(context.Background(), far); err != nil {
		t.Fatal(err)
	}
}

func safetyFixture(t GinkgoTInterface) (*v1alpha1.FenceAgentsRemediation, v1alpha1.SafetyConfig, *corev1.Secret) {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(server.Close)
	name := "credentials"
	return &v1alpha1.FenceAgentsRemediation{ObjectMeta: metav1.ObjectMeta{Name: "worker-a", Namespace: "bmc", UID: "far-a", Generation: 1, Annotations: safetyIdentityAnnotations("worker-a", "node-a", safetySystemUUID)}, Spec: v1alpha1.FenceAgentsRemediationSpec{
		SafetyPolicy: v1alpha1.RedfishOffV1,
		Agent:        "fence_redfish", RetryCount: 1, RetryInterval: metav1.Duration{Duration: time.Second}, Timeout: metav1.Duration{Duration: time.Second}, RemediationStrategy: v1alpha1.OutOfServiceTaintRemediationStrategy,
		SharedParameters: map[v1alpha1.ParameterName]string{"--action": "off", "--ip": "bmc.example", "--systems-uri": "/redfish/v1/Systems/1"}, SharedSecretName: &name,
	}}, v1alpha1.SafetyConfig{Enabled: true, CredentialNamespace: "bmc", TrustedNHCCreator: "system:serviceaccount:operators:nhc", CA: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})}, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "bmc", UID: "secret-a", ResourceVersion: "1"}, Data: map[string][]byte{"username": []byte("user"), "password": []byte("password")}}
}

func safetyIdentityAnnotations(name, uid, uuid string) map[string]string {
	return map[string]string{commonAnnotations.NodeNameAnnotation: name, commonAnnotations.NodeUIDAnnotation: uid, commonAnnotations.NodeSystemUUIDAnnotation: uuid}
}

var _ = Describe("Safety remediation logs", func() {
	It("records persisted lifecycle transitions without leaking configuration", func() {
		t := GinkgoT()
		h := newSafetyHarness(t, 1, nil)
		far, config, secret := safetyFixture(t)
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer GinkgoRecover()
			Expect(r.Method).To(Equal(http.MethodGet))
			_, _ = fmt.Fprintf(w, `{"UUID":"%s","PowerState":"Off"}`, safetySystemUUID)
		}))
		DeferCleanup(server.Close)
		endpoint, err := url.Parse(server.URL)
		Expect(err).NotTo(HaveOccurred())
		host, port, err := net.SplitHostPort(endpoint.Host)
		Expect(err).NotTo(HaveOccurred())
		far.Spec.SharedParameters["--ip"], far.Spec.SharedParameters["--ipport"] = host, port
		config.CA = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
		secret.Data = map[string][]byte{"username": []byte("private-user"), "password": []byte("private-password")}
		secret.ResourceVersion = ""
		Expect(h.client.Create(ctx, secret)).To(Succeed())
		current := h.getFAR(t)
		current.Spec, current.Annotations = far.Spec, far.Annotations
		current.Finalizers = append(current.Finalizers, "example.com/log-holder")
		Expect(h.client.Update(ctx, current)).To(Succeed())
		h.reconciler.Safety, h.reconciler.APIReader = config, h.client
		var records []string
		h.reconciler.Log = funcr.NewJSON(func(line string) {
			if strings.Contains(line, `"phase":`) {
				records = append(records, line)
			}
		}, funcr.Options{})
		for range 6 {
			h.reconcile(t)
			if isReleased(h.getFAR(t)) {
				break
			}
		}
		Expect(isReleased(h.getFAR(t))).To(BeTrue())
		count := len(records)
		h.reconcile(t)
		h.reconcile(t)
		Expect(records).To(HaveLen(count), "a completed release must not generate repeated lifecycle messages")
		Expect(h.client.Delete(ctx, h.getFAR(t))).To(Succeed())
		for range 3 {
			h.reconcile(t)
		}
		Expect(h.getFAR(t).Finalizers).To(ConsistOf("example.com/log-holder"))
		var phases []string
		for _, line := range records {
			var entry map[string]any
			Expect(json.Unmarshal([]byte(line), &entry)).To(Succeed())
			Expect(entry).To(HaveKey("remediation"))
			Expect(entry).To(HaveKeyWithValue("node", "worker-a"))
			Expect(entry).To(HaveKeyWithValue("nodeUID", "node-a"))
			phases = append(phases, entry["phase"].(string))
		}
		Expect(phases).To(Equal([]string{"TargetBound", "ActionAttemptReserved", "RedfishOffVerified", "IdentityBoundRelease", "CleanedUp"}))
		joined := strings.Join(records, "\n")
		for _, sensitive := range []string{"private-user", "private-password", server.URL, "BEGIN CERTIFICATE", "TrustDigest", "Credential"} {
			Expect(joined).NotTo(ContainSubstring(sensitive))
		}
	})

	It("logs a refusal once without including its sensitive error text", func() {
		t := GinkgoT()
		h := newSafetyHarness(t, 1, nil)
		enableSafety(t, h)
		var records []string
		h.reconciler.Log = funcr.NewJSON(func(line string) {
			if strings.Contains(line, `"phase":`) {
				records = append(records, line)
			}
		}, funcr.Options{})
		far := h.getFAR(t)
		cause := errors.New("private-password https://user:token@bmc-private.example/?secret=config")
		_, err := h.reconciler.failSafety(ctx, far, cause)
		Expect(err).To(MatchError(cause))
		_, err = h.reconciler.failSafety(ctx, h.getFAR(t), cause)
		Expect(err).To(MatchError(cause))
		Expect(records).To(HaveLen(1))
		var entry map[string]any
		Expect(json.Unmarshal([]byte(records[0]), &entry)).To(Succeed())
		Expect(entry).To(HaveKeyWithValue("phase", "SafetyCheckRefused"))
		Expect(entry).To(HaveKey("errorType"))
		for _, sensitive := range []string{"private-password", "user:token", "bmc-private.example", "secret=config"} {
			Expect(records[0]).NotTo(ContainSubstring(sensitive))
		}
	})
})

func (h *safetyHarness) node(t GinkgoTInterface) *corev1.Node {
	t.Helper()
	node := &corev1.Node{}
	if err := h.client.Get(context.Background(), client.ObjectKey{Name: h.far.Name}, node); err != nil {
		t.Fatal(err)
	}
	return node
}

func (h *safetyHarness) execute(t GinkgoTInterface) {
	t.Helper()
	h.reconcile(t)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		condition := meta.FindStatusCondition(h.getFAR(t).Status.Conditions, utils.FenceAgentActionSucceededType)
		if condition != nil && condition.Status != metav1.ConditionUnknown {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("executor did not finish")
}

func (h *safetyHarness) seedSuccess(t GinkgoTInterface) {
	t.Helper()
	far := h.getFAR(t)
	utils.UpdateConditions(utils.FenceAgentSucceeded, far, logr.Discard())
	if err := h.client.Status().Update(context.Background(), far); err != nil {
		t.Fatal(err)
	}
}

func (h *safetyHarness) recreateNode(t GinkgoTInterface) {
	t.Helper()
	node := h.node(t)
	if err := h.client.Delete(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	node.ResourceVersion = ""
	node.UID = "node-b"
	if err := h.client.Create(context.Background(), node); err != nil {
		t.Fatal(err)
	}
}

func hasOOS(node *corev1.Node) bool {
	taint := utils.CreateOutOfServiceTaint()
	return utils.TaintExists(node.Spec.Taints, &taint)
}

type failingRemediationList struct{ client.Reader }

func (r failingRemediationList) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if _, ok := list.(*v1alpha1.FenceAgentsRemediationList); ok {
		return fmt.Errorf("remediation list unavailable")
	}
	return r.Reader.List(ctx, list, opts...)
}

var _ = Describe("Safety-mode executor controls", func() {
	for _, fail := range []bool{false, true} {
		name := "success"
		var runnerErr error
		if fail {
			name = "failure"
			runnerErr = errors.New("runner failed")
		}
		It(name, func() {
			t := GinkgoT()
			h := newSafetyHarness(t, 1, runnerErr)
			h.execute(t)
			h.reconcile(t)
			if h.calls.Load() != 1 || hasOOS(h.node(t)) == fail {
				t.Fatal("executor control failed")
			}
		})
	}
})

var _ = It("rejects a zero retry budget without releasing the Node", func() {
	t := GinkgoT()
	h := newSafetyHarness(t, 0, nil)
	enableSafety(t, h)
	if _, err := h.reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(h.far)}); err == nil {
		t.Error("invalid retry count accepted")
	}
	if h.calls.Load() != 0 {
		t.Fatal("retry-zero control unexpectedly executed")
	}
	if hasOOS(h.node(t)) {
		t.Error("zero executions released node")
	}
})

var _ = It("does not release a replacement Node using persisted success", func() {
	t := GinkgoT()
	h := newSafetyHarness(t, 1, nil)
	enableSafety(t, h)
	h.seedSuccess(t)
	h.recreateNode(t)
	_, _ = h.reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(h.far)})
	if h.calls.Load() != 0 {
		t.Fatal("persisted-success control executed")
	}
	if hasOOS(h.node(t)) {
		t.Error("persisted success released replacement UID")
	}
})

var _ = It("cleans up the bound Node UID when a duplicate UUID exists", func() {
	t := GinkgoT()
	h := newSafetyHarness(t, 1, nil)
	enableSafety(t, h)
	node := h.node(t)
	node.Spec.Taints = []corev1.Taint{utils.CreateOutOfServiceTaint()}
	if err := h.client.Update(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	other := node.DeepCopy()
	other.Name = "worker-b"
	other.UID = "node-b"
	other.ResourceVersion = ""
	if err := h.client.Create(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	if err := h.client.Delete(context.Background(), h.getFAR(t)); err != nil {
		t.Fatal(err)
	}
	h.reconcile(t)
	if hasOOS(h.node(t)) {
		t.Fatal("duplicate UUID blocked bound-UID cleanup")
	}
	if err := h.client.Get(context.Background(), client.ObjectKey{Name: other.Name}, other); err != nil {
		t.Fatal(err)
	}
	if !hasOOS(other) {
		t.Fatal("cleanup touched duplicate-UUID Node with a different UID")
	}
})

var _ = It("refuses cleanup outside the credential namespace", func() {
	t := GinkgoT()
	h := newSafetyHarness(t, 1, nil)
	enableSafety(t, h)
	far := h.getFAR(t)
	far.Namespace = "foreign"
	far.ResourceVersion = ""
	far.UID = "foreign-far"
	if err := h.client.Create(context.Background(), far); err != nil {
		t.Fatal(err)
	}
	node := h.node(t)
	node.Spec.Taints = []corev1.Taint{utils.CreateOutOfServiceTaint()}
	if err := h.client.Update(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	if err := h.client.Delete(context.Background(), far); err != nil {
		t.Fatal(err)
	}
	if _, err := h.reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(far)}); err == nil {
		t.Error("foreign namespace cleanup accepted")
	}
	if !hasOOS(h.node(t)) {
		t.Fatal("foreign namespace removed selected Node taint")
	}
})

var _ = It("refuses cleanup with a changed snapshot", func() {
	t := GinkgoT()
	h := newSafetyHarness(t, 1, nil)
	enableSafety(t, h)
	far := h.getFAR(t)
	snapshot, err := v1alpha1.NewSafetySnapshot(far, v1alpha1.SafetyTarget{})
	if err != nil {
		t.Fatal(err)
	}
	far.Status.SafetySnapshot = &snapshot
	if err := h.client.Status().Update(context.Background(), far); err != nil {
		t.Fatal(err)
	}
	far = h.getFAR(t)
	far.Spec.NodeParameters["--ip"]["worker-a"] = "other.example"
	if err := h.client.Update(context.Background(), far); err != nil {
		t.Fatal(err)
	}
	node := h.node(t)
	node.Spec.Taints = []corev1.Taint{utils.CreateOutOfServiceTaint()}
	if err := h.client.Update(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	if err := h.client.Delete(context.Background(), h.getFAR(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := h.reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(h.far)}); err == nil {
		t.Error("cleanup accepted changed effective spec")
	}
	if !hasOOS(h.node(t)) {
		t.Fatal("cleanup changed taints after spec mutation")
	}
})

var _ = It("does not remove taints from a replacement Node", func() {
	t := GinkgoT()
	h := newSafetyHarness(t, 1, nil)
	enableSafety(t, h)
	h.seedSuccess(t)
	h.recreateNode(t)
	node := h.node(t)
	node.Spec.Taints = []corev1.Taint{utils.CreateOutOfServiceTaint()}
	if err := h.client.Update(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	if err := h.client.Delete(context.Background(), h.getFAR(t)); err != nil {
		t.Fatal(err)
	}
	h.reconcile(t)
	if !hasOOS(h.node(t)) {
		t.Error("cleanup removed replacement taint")
	}
})

var _ = It("cleans up a terminating safety remediation after the mode is disabled", func() {
	t := GinkgoT()
	h := newSafetyHarness(t, 1, nil)
	enableSafety(t, h)
	node := h.node(t)
	node.Spec.Taints = []corev1.Taint{utils.CreateOutOfServiceTaint()}
	if err := h.client.Update(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	if err := h.client.Delete(context.Background(), h.getFAR(t)); err != nil {
		t.Fatal(err)
	}
	h.reconciler.Safety = v1alpha1.SafetyConfig{}
	h.reconcile(t)
	if hasOOS(h.node(t)) {
		t.Error("out-of-service taint left on the Node")
	}
	if err := h.client.Get(context.Background(), client.ObjectKeyFromObject(h.far), &v1alpha1.FenceAgentsRemediation{}); !apierrors.IsNotFound(err) {
		t.Errorf("remediation still present: %v", err)
	}
})

var _ = It("keeps taints when a terminating remediation has a changed snapshot", func() {
	t := GinkgoT()
	h := newSafetyHarness(t, 1, nil)
	enableSafety(t, h)
	node := h.node(t)
	node.Spec.Taints = []corev1.Taint{utils.CreateOutOfServiceTaint()}
	if err := h.client.Update(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	far := h.getFAR(t)
	far.Status.SafetySnapshot = &v1alpha1.SafetySnapshot{FARUID: string(far.UID), SpecDigest: "persisted-for-another-spec"}
	if err := h.client.Status().Update(context.Background(), far); err != nil {
		t.Fatal(err)
	}
	if err := h.client.Delete(context.Background(), h.getFAR(t)); err != nil {
		t.Fatal(err)
	}
	h.reconciler.Safety = v1alpha1.SafetyConfig{}
	if _, err := h.reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(h.far)}); err == nil {
		t.Error("cleanup accepted a snapshot that differs from the remediation")
	}
	if !hasOOS(h.node(t)) {
		t.Error("out-of-service taint removed although the snapshot differs")
	}
	if !controllerutil.ContainsFinalizer(h.getFAR(t), v1alpha1.FARFinalizer) {
		t.Error("finalizer removed although the snapshot differs")
	}
})

var _ = Describe("Safety-mode shared-Node cleanup", func() {
	for _, tc := range []struct {
		name                         string
		otherReleased, otherDeleting bool
		otherUnbound, otherLegacy    bool
		otherNodeUID                 string
		listFails                    bool
		wantTaints, wantFinalizer    bool
	}{
		{name: "other remediation still running", wantTaints: true},
		{name: "other remediation released", otherReleased: true, wantTaints: true},
		{name: "other remediation deleted with it", otherDeleting: true},
		{name: "remediation list unavailable", listFails: true, wantTaints: true, wantFinalizer: true},
		{name: "other remediation never got its finalizer", otherUnbound: true},
		{name: "other remediation is not a safety remediation", otherLegacy: true},
		{name: "other remediation bound to another Node", otherNodeUID: "node-b"},
	} {
		It(tc.name, func() {
			t := GinkgoT()
			h := newSafetyHarness(t, 1, nil)
			enableSafety(t, h)
			node := h.node(t)
			node.Spec.Taints = []corev1.Taint{utils.CreateOutOfServiceTaint(), utils.CreateRemediationTaint()}
			if err := h.client.Update(context.Background(), node); err != nil {
				t.Fatal(err)
			}
			other := h.getFAR(t).DeepCopy()
			other.ObjectMeta = metav1.ObjectMeta{Name: "worker-a-escalated", Namespace: other.Namespace, Annotations: other.Annotations, Finalizers: []string{v1alpha1.FARFinalizer}}
			other.Status = v1alpha1.FenceAgentsRemediationStatus{}
			if tc.otherUnbound {
				other.Finalizers = nil
			}
			if tc.otherLegacy {
				other.Spec.SafetyPolicy = ""
			}
			if tc.otherNodeUID != "" {
				other.Annotations = map[string]string{}
				for key, value := range h.getFAR(t).Annotations {
					other.Annotations[key] = value
				}
				other.Annotations[commonAnnotations.NodeUIDAnnotation] = tc.otherNodeUID
			}
			if err := h.client.Create(context.Background(), other); err != nil {
				t.Fatal(err)
			}
			if tc.otherReleased {
				other.Status.OffProof = &v1alpha1.OffProof{ConfirmedAt: metav1.Now()}
				meta.SetStatusCondition(&other.Status.Conditions, metav1.Condition{Type: commonConditions.SucceededType, Status: metav1.ConditionTrue, Reason: "IdentityBoundRelease"})
				if err := h.client.Status().Update(context.Background(), other); err != nil {
					t.Fatal(err)
				}
			}
			if tc.otherDeleting {
				if err := h.client.Delete(context.Background(), other); err != nil {
					t.Fatal(err)
				}
			}
			if err := h.client.Delete(context.Background(), h.getFAR(t)); err != nil {
				t.Fatal(err)
			}
			if tc.listFails {
				h.reconciler.APIReader = failingRemediationList{Reader: h.client}
			}
			_, err := h.reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(h.far)})
			if tc.listFails && err == nil {
				t.Error("cleanup went ahead without knowing the other remediations")
			}
			taints := h.node(t).Spec.Taints
			if hasTaints := len(taints) == 2; hasTaints != tc.wantTaints {
				t.Errorf("taints kept=%t, want %t (%v)", hasTaints, tc.wantTaints, taints)
			}
			current := &v1alpha1.FenceAgentsRemediation{}
			getErr := h.client.Get(context.Background(), client.ObjectKeyFromObject(h.far), current)
			if hasFinalizer := getErr == nil && controllerutil.ContainsFinalizer(current, v1alpha1.FARFinalizer); hasFinalizer != tc.wantFinalizer {
				t.Errorf("finalizer kept=%t, want %t (get error %v)", hasFinalizer, tc.wantFinalizer, getErr)
			}
		})
	}
})

type safetyUpdateClient struct {
	client.Client
	before func(client.Object) error
}

func (c safetyUpdateClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	if err := c.before(obj); err != nil {
		return err
	}
	return c.Client.Update(ctx, obj, opts...)
}

type safetyIdentityReader struct {
	client.Reader
	mode  string
	calls *atomic.Int32
}

func (r safetyIdentityReader) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if nodes, ok := list.(*corev1.NodeList); ok {
		r.calls.Add(1)
		if len(opts) != 0 {
			return fmt.Errorf("identity List must not use selectors")
		}
		if r.mode == "forbidden" {
			return apierrors.NewForbidden(schema.GroupResource{Resource: "nodes"}, "", fmt.Errorf("list denied"))
		}
		if err := r.Reader.List(ctx, nodes); err != nil {
			return err
		}
		switch r.mode {
		case "missing":
			nodes.Items = nil
		case "partial":
			nodes.Continue = "remaining-nodes"
		case "remaining-count":
			remaining := int64(1)
			nodes.RemainingItemCount = &remaining
		case "replacement":
			for i := range nodes.Items {
				if nodes.Items[i].Name == "worker-a" {
					nodes.Items[i].UID = "listed-replacement"
				}
			}
		}
		return nil
	}
	return r.Reader.List(ctx, list, opts...)
}

var _ = It("keeps retry spacing across a restart and status serialization", func() {
	t := GinkgoT()
	h := newSafetyHarness(t, 2, nil)
	far, config, secret := safetyFixture(t)
	far.Spec.RetryCount = 2
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests.Add(1); w.WriteHeader(http.StatusUnauthorized) }))
	defer server.Close()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	host, port, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		t.Fatal(err)
	}
	far.Spec.SharedParameters["--ip"] = host
	far.Spec.SharedParameters["--ipport"] = port
	config.CA = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	current := h.getFAR(t)
	current.Spec = far.Spec
	current.Annotations = far.Annotations
	if err := h.client.Update(context.Background(), current); err != nil {
		t.Fatal(err)
	}
	secret.ResourceVersion = ""
	if err := h.client.Create(context.Background(), secret); err != nil {
		t.Fatal(err)
	}
	h.client = interceptor.NewClient(h.client.(client.WithWatch), interceptor.Funcs{
		SubResourceUpdate: func(ctx context.Context, c client.Client, name string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
			data, err := json.Marshal(obj)
			if err != nil {
				return err
			}
			if err := json.Unmarshal(data, obj); err != nil {
				return err
			}
			return c.SubResource(name).Update(ctx, obj, opts...)
		},
	})
	h.reconciler.Client = h.client
	h.reconciler.Safety = config
	h.reconciler.APIReader = h.client
	for range 2 {
		h.reconcile(t)
	}
	time.Sleep(time.Until(time.Now().Truncate(time.Second).Add(time.Second + 900*time.Millisecond)))
	attemptedAt := time.Now()
	h.reconcile(t)
	if h.getFAR(t).Status.SafetyAttempts != 1 || requests.Load() != 1 {
		t.Fatal("first reserved attempt control failed")
	}
	h.reconciler = &FenceAgentsRemediationReconciler{Client: h.client, APIReader: h.client, Safety: config, Log: h.reconciler.Log}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(h.far)}
	result, err := h.reconciler.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.RequeueAfter <= 0 || requests.Load() != 1 || h.getFAR(t).Status.SafetyAttempts != 1 {
		t.Fatal("restart ignored persisted retry interval")
	}
	if deadline := time.Now().Add(result.RequeueAfter); deadline.Before(attemptedAt.Add(far.Spec.RetryInterval.Duration)) {
		t.Errorf("serialized retry deadline %s precedes full interval after %s", deadline, attemptedAt)
	}
	early := attemptedAt.Truncate(time.Second).Add(far.Spec.RetryInterval.Duration + 50*time.Millisecond)
	time.Sleep(time.Until(early))
	if time.Since(attemptedAt) >= far.Spec.RetryInterval.Duration {
		t.Fatal("early retry control missed its observation window")
	}
	result, err = h.reconciler.Reconcile(context.Background(), req)
	if err != nil || result.RequeueAfter <= 0 || requests.Load() != 1 || h.getFAR(t).Status.SafetyAttempts != 1 {
		t.Errorf("wire round-trip allowed an early retry: requests=%d attempts=%d error=%v", requests.Load(), h.getFAR(t).Status.SafetyAttempts, err)
	}
	next := h.getFAR(t).Status.SafetyLastAttempt.Add(far.Spec.RetryInterval.Duration + 10*time.Millisecond)
	time.Sleep(time.Until(next))
	_, err = h.reconciler.Reconcile(context.Background(), req)
	if err == nil || requests.Load() != 2 || h.getFAR(t).Status.SafetyAttempts != 2 {
		t.Fatal("elapsed retry interval did not permit the next reserved attempt")
	}
})

var _ = Describe("Safety-mode controller", func() {
	for _, tc := range []struct {
		name, mutation, nodeList                         string
		alreadyOff, wrongUUID, stillOn, casRace, restart bool
		recovered, timeoutToggled                        bool
		wantRelease                                      bool
	}{
		{name: "matching identity Off", wantRelease: true},
		{name: "unique unfiltered Node list", nodeList: "unique", wantRelease: true},
		{name: "duplicate known UUID", nodeList: "duplicate"},
		{name: "duplicate uppercase known UUID", nodeList: "uppercase-duplicate"},
		{name: "Node list forbidden", nodeList: "forbidden"},
		{name: "Node list incomplete", nodeList: "partial"},
		{name: "Node list has remaining items", nodeList: "remaining-count"},
		{name: "bound target missing from list", nodeList: "missing"},
		{name: "bound target replaced in list", nodeList: "replacement"},
		{name: "matching already Off", alreadyOff: true, wantRelease: true},
		{name: "restart with proof rechecks", restart: true, wantRelease: true},
		{name: "Node recovered by hand after release", recovered: true, wantRelease: true},
		{name: "NHC timeout set and cleared after release", recovered: true, timeoutToggled: true, wantRelease: true},
		{name: "wrong BMC UUID", wrongUUID: true},
		{name: "accepted but On", stillOn: true},
		{name: "Unicode-aliased BMC power", mutation: "unicode-power"},
		{name: "Node replaced during precheck", mutation: "node-before-reset"},
		{name: "Node replaced during final identity GET", mutation: "node-during-final-identity"},
		{name: "duplicate UUID during final identity GET", mutation: "duplicate-during-final-identity"},
		{name: "duplicate UUID before release", mutation: "duplicate-before-release"},
		{name: "duplicate UUID during fresh Off", mutation: "duplicate-during-release"},
		{name: "Node replaced after reset", mutation: "node-after-reset"},
		{name: "Node replaced before release", mutation: "node-before-release"},
		{name: "Node replaced during fresh Off", mutation: "node-during-release"},
		{name: "Secret version changed before reset", mutation: "secret-before-reset"},
		{name: "Secret version changed before release", mutation: "secret-before-release"},
		{name: "spec target changed before reset", mutation: "target-before-reset"},
		{name: "spec target changed before release", mutation: "target-before-release"},
		{name: "CAS conflicts with replacement", casRace: true},
	} {
		It(tc.name, func() {
			t := GinkgoT()
			h := newSafetyHarness(t, 1, nil)
			far, config, secret := safetyFixture(t)
			if tc.recovered {
				far.Spec.RetryCount = 2
			}
			var resets, reads atomic.Int32
			var poweredOnAgain atomic.Bool
			mutate := func(kind string) {
				switch kind {
				case "node":
					node := h.node(t)
					if node.UID == "node-b" {
						return
					}
					if err := h.client.Delete(context.Background(), node); err != nil {
						t.Error(err)
						return
					}
					node.UID = "node-b"
					node.ResourceVersion = ""
					node.Spec.Taints = nil
					if err := h.client.Create(context.Background(), node); err != nil {
						t.Error(err)
					}
				case "duplicate":
					other := h.node(t).DeepCopy()
					other.Name = "worker-b"
					other.UID = "node-b"
					other.ResourceVersion = ""
					if err := h.client.Create(context.Background(), other); err != nil {
						t.Error(err)
					}
				case "secret":
					current := &corev1.Secret{}
					if err := h.client.Get(context.Background(), client.ObjectKeyFromObject(secret), current); err != nil {
						t.Error(err)
						return
					}
					current.Data["password"] = []byte("changed")
					if err := h.client.Update(context.Background(), current); err != nil {
						t.Error(err)
					}
				case "target":
					current := h.getFAR(t)
					current.Spec.SharedParameters["--ip"] = "other.example"
					if err := h.client.Update(context.Background(), current); err != nil {
						t.Error(err)
					}
				}
			}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer GinkgoRecover()
				if r.Method == http.MethodPost {
					resets.Add(1)
					if tc.mutation == "node-after-reset" {
						mutate("node")
					}
					w.WriteHeader(http.StatusAccepted)
					return
				}
				count := reads.Add(1)
				if count == 1 {
					switch tc.mutation {
					case "node-before-reset":
						mutate("node")
					case "secret-before-reset":
						mutate("secret")
					case "target-before-reset":
						mutate("target")
					}
				}
				if count == 2 && tc.mutation == "node-during-final-identity" {
					mutate("node")
				}
				if count == 2 && tc.mutation == "duplicate-during-final-identity" {
					mutate("duplicate")
				}
				if count == 4 && tc.mutation == "node-during-release" {
					mutate("node")
				}
				if count == 4 && tc.mutation == "duplicate-during-release" {
					mutate("duplicate")
				}
				uuid, power := safetySystemUUID, "On"
				if tc.wrongUUID {
					uuid = "aabbccdd-eeff-4011-8233-445566778899"
				}
				if tc.alreadyOff || resets.Load() > 0 && !tc.stillOn {
					power = "Off"
				}
				if poweredOnAgain.Load() {
					power = "On"
				}
				if tc.mutation == "unicode-power" {
					_, _ = w.Write([]byte(`{"UUID":"` + safetySystemUUID + `","PowerState":"On","Powerſtate":"Off","Actions":{"#ComputerSystem.Reset":{"target":"/redfish/v1/Systems/1/Actions/ComputerSystem.Reset"}}}`))
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"UUID": uuid, "PowerState": power, "Actions": map[string]any{"#ComputerSystem.Reset": map[string]string{"target": "/redfish/v1/Systems/1/Actions/ComputerSystem.Reset"}}})
			}))
			defer server.Close()
			parsed, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			host, port, err := net.SplitHostPort(parsed.Host)
			if err != nil {
				t.Fatal(err)
			}
			far.Spec.SharedParameters["--ip"] = host
			far.Spec.SharedParameters["--ipport"] = port
			config.CA = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
			current := h.getFAR(t)
			current.Spec = far.Spec
			current.Annotations = far.Annotations
			if err := h.client.Update(context.Background(), current); err != nil {
				t.Fatal(err)
			}
			secret.ResourceVersion = ""
			if err := h.client.Create(context.Background(), secret); err != nil {
				t.Fatal(err)
			}
			h.reconciler.Safety = config
			h.reconciler.APIReader = h.client
			var listCalls atomic.Int32
			if tc.nodeList != "" {
				if tc.nodeList == "duplicate" || tc.nodeList == "uppercase-duplicate" {
					other := h.node(t).DeepCopy()
					other.Name = "worker-b"
					other.UID = "node-b"
					other.ResourceVersion = ""
					if tc.nodeList == "uppercase-duplicate" {
						other.Status.NodeInfo.SystemUUID = strings.ToUpper(safetySystemUUID)
					}
					if err := h.client.Create(context.Background(), other); err != nil {
						t.Fatal(err)
					}
				}
				h.reconciler.APIReader = safetyIdentityReader{Reader: h.client, mode: tc.nodeList, calls: &listCalls}
			}
			if tc.casRace {
				h.reconciler.Client = safetyUpdateClient{Client: h.client, before: func(obj client.Object) error {
					if node, ok := obj.(*corev1.Node); ok && hasOOS(node) {
						mutate("node")
						return apierrors.NewConflict(schema.GroupResource{Resource: "nodes"}, node.Name, fmt.Errorf("concurrent replacement"))
					}
					return nil
				}}
			}
			var lastErr error
			for range 8 {
				_, lastErr = h.reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(h.far)})
				state := h.getFAR(t)

				if state.Status.SafetyAttempts < state.Spec.RetryCount && meta.IsStatusConditionFalse(state.Status.Conditions, commonConditions.SucceededType) {
					t.Errorf("Succeeded=False with attempts left: %+v", meta.FindStatusCondition(state.Status.Conditions, commonConditions.SucceededType))
				}
				if state.Status.OffProof != nil && !hasOOS(h.node(t)) {
					switch tc.mutation {
					case "node-before-release":
						mutate("node")
					case "duplicate-before-release":
						mutate("duplicate")
					case "secret-before-release":
						mutate("secret")
					case "target-before-release":
						mutate("target")
					}
					if tc.restart {
						h.reconciler = &FenceAgentsRemediationReconciler{Client: h.client, APIReader: h.client, Safety: config, Log: h.reconciler.Log, Recorder: h.reconciler.Recorder}
					}
				}
				if lastErr != nil || hasOOS(h.node(t)) {
					break
				}
			}
			if tc.nodeList != "" && listCalls.Load() == 0 {
				t.Error("identity did not use an uncached unfiltered Node list")
			}
			if tc.nodeList != "" && tc.nodeList != "unique" && resets.Load() != 0 {
				t.Error("ambiguous or unavailable Node inventory permitted a power action")
			}
			if hasOOS(h.node(t)) != tc.wantRelease {
				t.Fatalf("released=%t wanted=%t error=%v reads=%d resets=%d", hasOOS(h.node(t)), tc.wantRelease, lastErr, reads.Load(), resets.Load())
			}
			if tc.wantRelease {
				state := h.getFAR(t)
				if state.Status.OffProof == nil || state.Status.SafetySnapshot == nil || state.Status.OffProof.Snapshot != *state.Status.SafetySnapshot || !meta.IsStatusConditionTrue(state.Status.Conditions, utils.FenceAgentActionSucceededType) {
					t.Fatal("release lacks identity-bound proof")
				}
				if tc.alreadyOff && resets.Load() != 0 {
					t.Fatal("already-Off reset")
				}
				if tc.restart && reads.Load() < 4 {
					t.Fatal("restart did not fresh-check BMC")
				}
			} else if lastErr == nil {
				t.Fatal("negative control did not hit refusal")
			}
			if tc.mutation == "unicode-power" {
				if lastErr == nil || !strings.Contains(lastErr.Error(), "ambiguous Redfish response") {
					t.Errorf("Unicode alias refusal is not a parser rejection: %v", lastErr)
				}
				if resets.Load() != 0 {
					t.Errorf("Unicode alias permitted ForceOff: %d", resets.Load())
				}
			}
			if tc.mutation == "node-before-reset" || tc.mutation == "node-during-final-identity" || tc.mutation == "duplicate-during-final-identity" || tc.mutation == "secret-before-reset" || tc.mutation == "target-before-reset" {
				if resets.Load() != 0 {
					t.Error("power action ran after a pre-action identity/configuration change")
				}
			}
			if tc.wantRelease {
				before := reads.Load()
				if _, err := h.reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(h.far)}); err != nil {
					t.Fatal(err)
				}
				if reads.Load() != before {
					t.Error("terminal success generated another BMC operation")
				}
				var calls atomic.Int32
				h.reconciler.APIReader = safetyIdentityReader{Reader: h.client, mode: "forbidden", calls: &calls}
				if _, err := h.reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(h.far)}); err == nil {
					t.Error("a failed Node read after release was not reported")
				}
				if state := h.getFAR(t); state.Status.OffProof == nil || !meta.IsStatusConditionTrue(state.Status.Conditions, commonConditions.SucceededType) {
					t.Error("a failed Node read after release cleared the Off proof or the success condition")
				}
				if !hasOOS(h.node(t)) || reads.Load() != before {
					t.Error("a failed Node read after release changed the release")
				}
			}
			if tc.recovered {
				h.reconciler.APIReader = h.client
				node := h.node(t)
				node.Spec.Taints = nil
				if err := h.client.Update(context.Background(), node); err != nil {
					t.Fatal(err)
				}
				poweredOnAgain.Store(true)
				resetsBefore := resets.Load()
				if tc.timeoutToggled {
					current := h.getFAR(t)
					current.Annotations[commonAnnotations.NhcTimedOut] = "now"
					if err := h.client.Update(context.Background(), current); err != nil {
						t.Fatal(err)
					}
					_, _ = h.reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(h.far)})
					current = h.getFAR(t)
					delete(current.Annotations, commonAnnotations.NhcTimedOut)
					if err := h.client.Update(context.Background(), current); err != nil {
						t.Fatal(err)
					}
				}

				for range 8 {
					_, _ = h.reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(h.far)})
					time.Sleep(300 * time.Millisecond)
				}
				if resets.Load() != resetsBefore {
					t.Error("a completed remediation powered the recovered Node off again")
				}
				if state := h.getFAR(t); state.Status.OffProof == nil || !meta.IsStatusConditionTrue(state.Status.Conditions, commonConditions.SucceededType) {
					t.Error("a completed remediation lost its Off proof or success after the Node was recovered")
				}
				if len(h.node(t).Spec.Taints) != 0 {
					t.Error("a completed remediation tainted the recovered Node again")
				}
			}
			if tc.wrongUUID {
				before := h.getFAR(t).ResourceVersion
				_, _ = h.reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(h.far)})
				if h.getFAR(t).ResourceVersion != before {
					t.Error("unchanged refusal generated another status event")
				}
			}
			if tc.stillOn && !meta.IsStatusConditionFalse(h.getFAR(t).Status.Conditions, commonConditions.SucceededType) {
				t.Error("a remediation out of attempts does not report Succeeded=False")
			}
			if h.calls.Load() != 0 {
				t.Fatal("safety mode executed generic subprocess")
			}
		})
	}
})
