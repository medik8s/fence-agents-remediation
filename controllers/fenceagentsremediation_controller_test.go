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
	"time"

	commonConditions "github.com/medik8s/common/pkg/conditions"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/medik8s/fence-agents-remediation/api/v1alpha1"
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
	timeoutDeletion  = 2 * time.Second // this timeout is used after all the other steps have finished successfully
	timeoutFinalizer = 1 * time.Second
	pollInterval     = 250 * time.Millisecond
)

var (
	faPodLabels  = map[string]string{"app.kubernetes.io/name": "fence-agents-remediation-operator"}
	log          = ctrl.Log.WithName("controllers-unit-test")
	underTestFAR = &v1alpha1.FenceAgentsRemediation{}
)

var _ = Describe("FAR Controller", func() {
	var (
		node *corev1.Node
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

	// default FenceAgentsRemediation CR

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
		farNoExecuteTaint := utils.CreateFARNoExecuteTaint()
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
			DeferCleanup(cleanupFar, context.Background(), underTestFAR)
		})

		// TODO: add more scenarios?
		When("creating valid FAR CR", func() {
			BeforeEach(func() {
				node = utils.GetNode("", workerNode)
				underTestFAR = getFenceAgentsRemediation(workerNode, fenceAgentIPMI, testShareParam, testNodeParam)
			})

			It("should have finalizer, taint, while the two VAs and one pod will be deleted", func() {
				By("Searching for remediation taint")
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(context.Background(), client.ObjectKey{Name: workerNode}, node)).To(Succeed())
					g.Expect(k8sClient.Get(context.Background(), client.ObjectKey{Name: workerNode, Namespace: defaultNamespace}, underTestFAR)).To(Succeed())
					g.Expect(storedCommand).To(ConsistOf([]string{"fence_ipmilan", "--lanplus", "--password=password", "--username=admin", "--action=reboot", "--ip=192.168.111.1", "--ipport=6233"}))
					g.Expect(utils.TaintExists(node.Spec.Taints, &farNoExecuteTaint)).To(BeTrue(), "remediation taint should exist")
				}, timeoutFinalizer, pollInterval).Should(Succeed())

				// If taint was added, then definitely the finalizer was added as well
				By("Having a finalizer if we have a remediation taint")
				Expect(controllerutil.ContainsFinalizer(underTestFAR, v1alpha1.FARFinalizer)).To(BeTrue())

				By("Not having any test pod")
				checkPodIsNotFound(testPodName, true)

				By("Verifying correct conditions for successfull remediation")
				Expect(underTestFAR.Status.LastUpdateTime).ToNot(BeNil())
				verifyStatusCondition(workerNode, commonConditions.ProcessingType, conditionStatusPointer(metav1.ConditionFalse))
				verifyStatusCondition(workerNode, utils.FenceAgentActionSucceededType, conditionStatusPointer(metav1.ConditionTrue))
				verifyStatusCondition(workerNode, commonConditions.SucceededType, conditionStatusPointer(metav1.ConditionTrue))
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
				}, timeoutFinalizer, pollInterval).Should(BeFalse(), "finalizer shouldn't be added")

				// If finalizer is missing, then a taint shouldn't be existed
				By("Not having remediation taint")
				Expect(utils.TaintExists(node.Spec.Taints, &farNoExecuteTaint)).To(BeFalse())

				By("Still having one test pod")
				checkPodIsNotFound(testPodName, false)

				By("Verifying correct conditions for unsuccessfull remediation")
				Expect(underTestFAR.Status.LastUpdateTime).ToNot(BeNil())
				verifyStatusCondition(dummyNode, commonConditions.ProcessingType, conditionStatusPointer(metav1.ConditionFalse))
				verifyStatusCondition(dummyNode, utils.FenceAgentActionSucceededType, conditionStatusPointer(metav1.ConditionFalse))
				verifyStatusCondition(dummyNode, commonConditions.SucceededType, conditionStatusPointer(metav1.ConditionFalse))
			})
		})

		Context("Fence Agent Failures", func() {
			BeforeEach(func() {
				node = utils.GetNode("", workerNode)
				mockError = errors.New("mock error")
				DeferCleanup(func() { mockError = nil })

				underTestFAR = getFenceAgentsRemediation(workerNode, fenceAgentIPMI, testShareParam, testNodeParam)
			})

			When("Fence Agent command fails", func() {
				BeforeEach(func() {
					underTestFAR.Spec.RetryCount = 3
				})
				It("should retry the fence agent command as configured and update the status accordingly", func() {
					Eventually(func(g Gomega) {
						g.Expect(k8sClient.Get(context.Background(), client.ObjectKey{Name: workerNode}, node)).To(Succeed())
						g.Expect(utils.TaintExists(node.Spec.Taints, &farNoExecuteTaint)).To(BeTrue(), "remediation taint should exist")
					}, timeoutFinalizer, pollInterval).Should(Succeed())

					By("Still having one test pod")
					checkPodIsNotFound(testPodName, false)

					By("Expected number of retries")
					Eventually(func() int {
						return plogs.CountOccurences("command failed")
					}).Should(Equal(3))

					By("Verifying correct conditions for un-successful remediation")
					Eventually(func(g Gomega) {
						g.Expect(k8sClient.Get(context.Background(), client.ObjectKey{Name: workerNode, Namespace: defaultNamespace}, underTestFAR)).To(Succeed())
					}).Should(Succeed())
					Expect(underTestFAR.Status.LastUpdateTime).ToNot(BeNil())
					verifyStatusCondition(workerNode, commonConditions.ProcessingType, conditionStatusPointer(metav1.ConditionFalse))
					verifyStatusCondition(workerNode, utils.FenceAgentActionSucceededType, conditionStatusPointer(metav1.ConditionFalse))
					verifyStatusCondition(dummyNode, commonConditions.SucceededType, conditionStatusPointer(metav1.ConditionFalse))

				})
			})
			When("Fence Agent command times out", func() {
				BeforeEach(func() {
					forcedDelay = 10 * time.Second
					underTestFAR.Spec.Timeout = metav1.Duration{Duration: 2 * time.Second}
				})
				It("should stop Fence Agent execution and update the status accordingly", func() {
					Eventually(func(g Gomega) {
						g.Expect(k8sClient.Get(context.Background(), client.ObjectKey{Name: workerNode}, node)).To(Succeed())
						g.Expect(utils.TaintExists(node.Spec.Taints, &farNoExecuteTaint)).To(BeTrue(), "remediation taint should exist")
					}, timeoutFinalizer, pollInterval).Should(Succeed())

					By("Still having one test pod")
					checkPodIsNotFound(testPodName, false)

					By("Context timeout occurred")
					Eventually(func() bool {
						return plogs.Contains("command timed out")
					}).Should(BeTrue())

					By("Verifying correct conditions for un-successful remediation")
					Eventually(func(g Gomega) {
						g.Expect(k8sClient.Get(context.Background(), client.ObjectKey{Name: workerNode, Namespace: defaultNamespace}, underTestFAR)).To(Succeed())
					}).Should(Succeed())

					Expect(underTestFAR.Status.LastUpdateTime).ToNot(BeNil())
					verifyStatusCondition(workerNode, commonConditions.ProcessingType, conditionStatusPointer(metav1.ConditionFalse))
					verifyStatusCondition(workerNode, utils.FenceAgentActionSucceededType, conditionStatusPointer(metav1.ConditionFalse))
					verifyStatusCondition(dummyNode, commonConditions.SucceededType, conditionStatusPointer(metav1.ConditionFalse))

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
			RetryCount: 1,
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
		deletionOptions := &client.DeleteOptions{GracePeriodSeconds: new(int64)}
		*deletionOptions.GracePeriodSeconds = 0
		Expect(k8sClient.Delete(context.Background(), podTest, deletionOptions)).To(Succeed())
	}
}

func checkPodIsNotFound(podName string, expected bool) {
	podKey := client.ObjectKey{
		Namespace: defaultNamespace,
		Name:      podName,
	}

	ConsistentlyWithOffset(1, func() bool {
		pod := &corev1.Pod{}
		err := k8sClient.Get(context.Background(), podKey, pod)
		return apierrors.IsNotFound(err)
	}, timeoutDeletion, pollInterval).Should(Equal(expected))
}

// verifyStatusCondition checks if the status condition is not set, and if it is set then it has an expected value
func verifyStatusCondition(nodeName, conditionType string, conditionStatus *metav1.ConditionStatus) {
	Eventually(func(g Gomega) {
		condition := meta.FindStatusCondition(underTestFAR.Status.Conditions, conditionType)
		if conditionStatus == nil {
			g.Expect(condition).To(BeNil(), "expected condition %v to not be set", conditionType)
		} else {
			g.Expect(condition).ToNot(BeNil(), "expected condition %v to be set", conditionType)
			g.Expect(condition.Status).To(Equal(*conditionStatus), "expected condition %v to have status %v", conditionType, *conditionStatus)
		}
	}, timeoutDeletion, pollInterval).Should(Succeed())
}

// cleanupFar removes FAR finalizer and deletes the FAR CR
func cleanupFar(ctx context.Context, far *v1alpha1.FenceAgentsRemediation) {
	// Remove finalizer
	far.ObjectMeta.Finalizers = []string{}
	Expect(k8sClient.Update(ctx, far)).To(Succeed())

	// Delete the FAR
	Expect(k8sClient.Delete(ctx, far)).To(Succeed())
}
