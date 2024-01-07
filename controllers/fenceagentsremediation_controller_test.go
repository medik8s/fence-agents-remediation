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
package controllers

import (
	"context"
	"errors"
	"fmt"
	"time"

	commonConditions "github.com/medik8s/common/pkg/conditions"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/medik8s/fence-agents-remediation/api/v1alpha1"
	"github.com/medik8s/fence-agents-remediation/pkg/cli"
	"github.com/medik8s/fence-agents-remediation/pkg/utils"
)

const (
	dummyNode      = "dummy-node"
	workerNode     = "worker-0"
	fenceAgentIPMI = "fence_ipmilan"
	farPodName     = "far-pod"
	testPodName    = "far-pod-test-1"
	vaName1        = "va-test-1"
	vaName2        = "va-test-2"

	// intervals
	timeoutPreRemediation  = "1s" // this timeout is used for the other steps that occur before remediation is completed
	timeoutPostRemediation = "2s" // this timeout is used for the other steps that occur after remediation is completed
	pollInterval           = "200ms"

	// eventSteps
	eventExixst    = "Verifying that event %s was created from"
	eventNotExixst = "Verifying that event %s was not created from"
)

var (
	faPodLabels = map[string]string{"app.kubernetes.io/name": "fence-agents-remediation-operator"}
	log         = ctrl.Log.WithName("controllers-unit-test")
)

var _ = Describe("FAR Controller", func() {
	var (
		node         *corev1.Node
		underTestFAR = &v1alpha1.FenceAgentsRemediation{}
	)

	invalidShareParam := map[v1alpha1.ParameterName]string{
		"--username": "admin",
		"--password": "password",
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

	Context("Functionality", func() {
		BeforeEach(func() {
			plogs.Clear()
			underTestFAR = getFenceAgentsRemediation(workerNode, fenceAgentIPMI, testShareParam, testNodeParam)
		})

		Context("buildFenceAgentParams", func() {
			When("FAR include different action than reboot", func() {
				It("should succeed with a warning", func() {
					invalidValTestFAR := getFenceAgentsRemediation(workerNode, fenceAgentIPMI, invalidShareParam, testNodeParam)
					invalidShareString, err := buildFenceAgentParams(invalidValTestFAR)
					Expect(err).NotTo(HaveOccurred())
					underTestFAR.ObjectMeta.Name = workerNode
					validShareString, err := buildFenceAgentParams(underTestFAR)
					Expect(err).NotTo(HaveOccurred())
					// Eventually buildFenceAgentParams would return the same shareParam
					Expect(invalidShareString).To(ConsistOf(validShareString))
				})
			})
			When("FAR CR's name doesn't match a node name", func() {
				It("should fail", func() {
					underTestFAR.ObjectMeta.Name = dummyNode
					_, err := buildFenceAgentParams(underTestFAR)
					Expect(err).To(HaveOccurred())
					Expect(err).To(Equal(errors.New(errorMissingNodeParams)))
				})
			})
			When("FAR CR's name does match a node name", func() {
				It("should succeed", func() {
					underTestFAR.ObjectMeta.Name = workerNode
					Expect(buildFenceAgentParams(underTestFAR)).Error().NotTo(HaveOccurred())
				})
			})
		})
	})

	Context("Reconcile", func() {
		farRemediationTaint := utils.CreateRemediationTaint()
		conditionStatusPointer := func(status metav1.ConditionStatus) *metav1.ConditionStatus { return &status }

		BeforeEach(func() {
			// Create two VAs and two pods, and at the end clean them up with DeferCleanup
			va1 := createVA(vaName1, workerNode)
			va2 := createVA(vaName2, workerNode)
			testPod := createRunningPod("far-test-1", testPodName, workerNode)
			DeferCleanup(cleanupTestedResources, va1, va2, testPod)

			farPod := createRunningPod("far-manager-test", farPodName, "")
			DeferCleanup(k8sClient.Delete, context.Background(), farPod)
		})

		JustBeforeEach(func() {
			// Create node, and FAR CR, and at the end clean them up with DeferCleanup
			Expect(k8sClient.Create(context.Background(), node)).To(Succeed())
			DeferCleanup(k8sClient.Delete, context.Background(), node)

			Expect(k8sClient.Create(context.Background(), underTestFAR)).To(Succeed())
			DeferCleanup(cleanupFar(), context.Background(), underTestFAR)

			// Sleep for a second to ensure dummy reconciliation has begun running before the unit tests
			time.Sleep(1 * time.Second)
		})

		When("creating valid FAR CR", func() {
			BeforeEach(func() {
				node = utils.GetNode("", workerNode)
				underTestFAR = getFenceAgentsRemediation(workerNode, fenceAgentIPMI, testShareParam, testNodeParam)
			})

			It("should have finalizer, taint, while the two VAs and one pod will be deleted", func() {
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

				underTestFAR = verifyPreRemediationSucceed(underTestFAR, workerNode, defaultNamespace, &farRemediationTaint)

				By("Not having any test pod")
				verifyPodDeleted(testPodName)

				By("Verifying correct conditions for successful remediation")
				verifyRemediationConditions(
					underTestFAR,
					workerNode,
					conditionStatusPointer(metav1.ConditionFalse), // ProcessingTypeStatus
					conditionStatusPointer(metav1.ConditionTrue),  // FenceAgentActionSucceededTypeStatus
					conditionStatusPointer(metav1.ConditionTrue))  // SucceededTypeStatus
				verifyEvent(corev1.EventTypeNormal, utils.EventReasonFenceAgentSucceeded, utils.EventMessageFenceAgentSucceeded)
				verifyEvent(corev1.EventTypeNormal, utils.EventReasonNodeRemediationCompleted, utils.EventMessageNodeRemediationCompleted)
			})
		})

		When("creating invalid FAR CR Name", func() {
			BeforeEach(func() {
				node = utils.GetNode("", workerNode)
				underTestFAR = getFenceAgentsRemediation(dummyNode, fenceAgentIPMI, testShareParam, testNodeParam)
			})

			It("should not have a finalizer nor taint, while the two VAs and one pod will remain", func() {
				By("Not finding a matching node to FAR CR's name")
				Expect(k8sClient.Get(context.Background(), client.ObjectKey{Name: underTestFAR.Name}, node)).To(Not(Succeed()))

				By("Not having finalizer")
				Consistently(func(g Gomega) bool {
					g.Expect(k8sClient.Get(context.Background(), client.ObjectKey{Name: underTestFAR.Name, Namespace: defaultNamespace}, underTestFAR)).To(Succeed())
					return controllerutil.ContainsFinalizer(underTestFAR, v1alpha1.FARFinalizer)
				}, timeoutPreRemediation, pollInterval).Should(BeFalse(), "finalizer shouldn't be added")
				verifyEvent(corev1.EventTypeWarning, utils.EventReasonCrNodeNotFound, utils.EventMessageCrNodeNotFound)
				verifyNoEvent(corev1.EventTypeNormal, utils.EventReasonAddFinalizer, utils.EventMessageAddFinalizer)

				// If finalizer is missing, then a taint shouldn't exist
				By("Not having remediation taint")
				Expect(utils.TaintExists(node.Spec.Taints, &farRemediationTaint)).To(BeFalse())
				verifyNoEvent(corev1.EventTypeNormal, utils.EventReasonAddRemediationTaint, utils.EventMessageAddRemediationTaint)

				By("Still having one test pod")
				verifyPodExists(testPodName)

				By("Verifying correct conditions for unsuccessful remediation")
				verifyRemediationConditions(
					underTestFAR,
					dummyNode,
					conditionStatusPointer(metav1.ConditionFalse), // ProcessingTypeStatus
					conditionStatusPointer(metav1.ConditionFalse), // FenceAgentActionSucceededTypeStatus
					conditionStatusPointer(metav1.ConditionFalse)) // SucceededTypeStatus
				verifyNoEvent(corev1.EventTypeNormal, utils.EventReasonFenceAgentSucceeded, utils.EventMessageFenceAgentSucceeded)
				verifyNoEvent(corev1.EventTypeNormal, utils.EventReasonNodeRemediationCompleted, utils.EventReasonNodeRemediationCompleted)
			})
		})

		Context("Fence agent failures", func() {
			BeforeEach(func() {
				plogs.Clear()
				node = utils.GetNode("", workerNode)

				underTestFAR = getFenceAgentsRemediation(workerNode, fenceAgentIPMI, testShareParam, testNodeParam)
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
					underTestFAR = verifyPreRemediationSucceed(underTestFAR, workerNode, defaultNamespace, &farRemediationTaint)

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
					underTestFAR = verifyPreRemediationSucceed(underTestFAR, workerNode, defaultNamespace, &farRemediationTaint)

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
					underTestFAR = verifyPreRemediationSucceed(underTestFAR, workerNode, defaultNamespace, &farRemediationTaint)

					By("Still having one test pod")
					verifyPodExists(testPodName)

					By("Reading the expected number of retries")
					Eventually(func() int {
						return plogs.CountOccurences(cli.FenceAgentFailedCommandMessage)
					}).Should(Equal(3))

					By("Verifying correct conditions for un-successful remediation")
					verifyRemediationConditions(
						underTestFAR,
						workerNode,
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
					underTestFAR = verifyPreRemediationSucceed(underTestFAR, workerNode, defaultNamespace, &farRemediationTaint)

					By("Still having one test pod")
					verifyPodExists(testPodName)

					By("Context timeout occurred")
					Eventually(func() bool {
						return plogs.Contains(cli.FenceAgentContextTimedOutMessage)
					}).Should(BeTrue(), "fence agent should have timed out")

					By("Verifying correct conditions for un-successful remediation")
					verifyRemediationConditions(
						underTestFAR,
						workerNode,
						conditionStatusPointer(metav1.ConditionFalse), // ProcessingTypeStatus
						conditionStatusPointer(metav1.ConditionFalse), // FenceAgentActionSucceededTypeStatus
						conditionStatusPointer(metav1.ConditionFalse)) // SucceededTypeStatus
					verifyNoEvent(corev1.EventTypeNormal, utils.EventReasonFenceAgentSucceeded, utils.EventMessageFenceAgentSucceeded)
				})
			})
		})
	})
})

// getFenceAgentsRemediation assigns the input to the FenceAgentsRemediation
func getFenceAgentsRemediation(nodeName, agent string, sharedparameters map[v1alpha1.ParameterName]string, nodeparameters map[v1alpha1.ParameterName]map[v1alpha1.NodeName]string) *v1alpha1.FenceAgentsRemediation {
	return &v1alpha1.FenceAgentsRemediation{
		ObjectMeta: metav1.ObjectMeta{Name: nodeName, Namespace: defaultNamespace},
		Spec: v1alpha1.FenceAgentsRemediationSpec{
			Agent:            agent,
			SharedParameters: sharedparameters,
			NodeParameters:   nodeparameters,
			// Set the retry count to the minimum for the majority of the tests
			RetryCount:    1,
			RetryInterval: metav1.Duration{Duration: 5 * time.Second},
			Timeout:       metav1.Duration{Duration: 60 * time.Second},
		},
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

// createVA creates new volume attachment and return its object
func createVA(vaName, unhealthyNodeName string) *storagev1.VolumeAttachment {
	va := &storagev1.VolumeAttachment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      vaName,
			Namespace: defaultNamespace,
		},
		Spec: storagev1.VolumeAttachmentSpec{
			Attacher: "foo",
			Source:   storagev1.VolumeAttachmentSource{},
			NodeName: unhealthyNodeName,
		},
	}
	foo := "foo"
	va.Spec.Source.PersistentVolumeName = &foo
	ExpectWithOffset(1, k8sClient.Create(context.Background(), va)).To(Succeed())
	return va
}

// cleanupTestedResources fetches all the resources that we have crated for the test
// and if they are still exist at the end of the test, then we clean them up for next test
func cleanupTestedResources(va1, va2 *storagev1.VolumeAttachment, pod *corev1.Pod) {
	for _, va := range []*storagev1.VolumeAttachment{va1, va2} {
		vaTest := &storagev1.VolumeAttachment{}
		key := client.ObjectKeyFromObject(va)
		if err := k8sClient.Get(context.Background(), key, vaTest); err == nil {
			log.Info("Cleanup: clean volume attachment", "va name", vaTest.Name)
			Expect(k8sClient.Delete(context.Background(), vaTest)).To(Succeed())
		}
	}

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
func verifyStatusCondition(far *v1alpha1.FenceAgentsRemediation, nodeName, conditionType string, conditionStatus *metav1.ConditionStatus) {
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
func verifyPreRemediationSucceed(underTestFAR *v1alpha1.FenceAgentsRemediation, nodeName, namespace string, taint *corev1.Taint) *v1alpha1.FenceAgentsRemediation {
	By("Searching for finalizer ")
	Expect(k8sClient.Get(context.Background(), client.ObjectKey{Name: nodeName, Namespace: namespace}, underTestFAR)).To(Succeed())
	Expect(controllerutil.ContainsFinalizer(underTestFAR, v1alpha1.FARFinalizer)).To(BeTrue())
	verifyEvent(corev1.EventTypeNormal, utils.EventReasonAddFinalizer, utils.EventMessageAddFinalizer)

	By("Searching for remediation taint if we have a finalizer")
	Eventually(func(g Gomega) {
		node := &corev1.Node{}
		g.Expect(k8sClient.Get(context.Background(), client.ObjectKey{Name: nodeName}, node)).To(Succeed())
		g.Expect(utils.TaintExists(node.Spec.Taints, taint)).To(BeTrue(), "remediation taint should exist")
	}, timeoutPreRemediation, pollInterval).Should(Succeed())
	verifyEvent(corev1.EventTypeNormal, utils.EventReasonAddRemediationTaint, utils.EventMessageAddRemediationTaint)

	return underTestFAR
}

func verifyEvent(eventType, eventReason, eventMessage string) {
	var eventRecorder *record.FakeRecorder
	if eventReason == utils.EventReasonFenceAgentSucceeded {
		By(fmt.Sprintf(eventExixst+" executer", eventReason))
		eventRecorder = fakeExecRecorder
	} else {
		By(fmt.Sprintf(eventExixst+" reconcile", eventReason))
		eventRecorder = fakeReconcileRecorder
	}
	isEventMatch := isEventOccurred(eventType, eventReason, eventMessage, eventRecorder)
	ExpectWithOffset(1, isEventMatch).To(BeTrue())
}

func verifyNoEvent(eventType, eventReason, eventMessage string) {
	var eventRecorder *record.FakeRecorder
	if eventReason == utils.EventReasonFenceAgentSucceeded {
		By(fmt.Sprintf(eventNotExixst+" executer", eventReason))
		eventRecorder = fakeExecRecorder
	} else {
		By(fmt.Sprintf(eventNotExixst+" reconcile", eventReason))
		eventRecorder = fakeReconcileRecorder
	}
	isEventMatch := isEventOccurred(eventType, eventReason, eventMessage, eventRecorder)
	ExpectWithOffset(1, isEventMatch).To(BeFalse())
}

// isEventOccurred checks whether an event has occoured
func isEventOccurred(eventType, eventReason, eventMessage string, recorder *record.FakeRecorder) bool {
	expected := fmt.Sprintf("%s %s [remediation] %s", eventType, eventReason, eventMessage)
	isEventMatch := false

	unMatchedEvents := make(chan string, len(recorder.Events))
	isDone := false
	for {
		select {
		case event := <-recorder.Events:
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
		recorder.Events <- unMatchedEvent
	}
	return isEventMatch
}

// clearEvents loop over the events channel until it is empty from events
func clearEvents() {
	for len(fakeReconcileRecorder.Events) > 0 {
		<-fakeReconcileRecorder.Events
	}
	for len(fakeExecRecorder.Events) > 0 {
		<-fakeExecRecorder.Events
	}
	log.Info("Cleanup: events list is empty")
}

func verifyRemediationConditions(far *v1alpha1.FenceAgentsRemediation, nodeName string, processingTypeConditionStatus, fenceAgentSuccededTypeConditionStatus, succededTypeConditionStatus *metav1.ConditionStatus) {
	EventuallyWithOffset(1, func(g Gomega) {
		ut := &v1alpha1.FenceAgentsRemediation{}
		g.Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(far), ut)).To(Succeed())
		g.Expect(ut.Status.LastUpdateTime).ToNot(BeNil())
		verifyStatusCondition(ut, nodeName, commonConditions.ProcessingType, processingTypeConditionStatus)
		verifyStatusCondition(ut, nodeName, utils.FenceAgentActionSucceededType, fenceAgentSuccededTypeConditionStatus)
		verifyStatusCondition(ut, nodeName, commonConditions.SucceededType, succededTypeConditionStatus)
	})
}

// cleanupFar deletes the FAR CR and waits until it is deleted. The function ignores if the CR is already deleted.
func cleanupFar() func(ctx context.Context, far *v1alpha1.FenceAgentsRemediation) error {
	return func(ctx context.Context, far *v1alpha1.FenceAgentsRemediation) error {
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
		clearEvents()
		return nil
	}
}
