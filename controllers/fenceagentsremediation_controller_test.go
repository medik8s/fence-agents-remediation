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
	"reflect"
	"sort"
	"time"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
)

var (
	faPodLabels = map[string]string{"app.kubernetes.io/name": "fence-agents-remediation-operator"}
	log         = ctrl.Log.WithName("controllers-unit-test")
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
	underTestFAR := getFenceAgentsRemediation(workerNode, fenceAgentIPMI, testShareParam, testNodeParam)

	Context("Functionality", func() {
		Context("buildFenceAgentParams", func() {
			When("FAR include different action than reboot", func() {
				It("should succeed with a warning", func() {
					invalidValTestFAR := getFenceAgentsRemediation(workerNode, fenceAgentIPMI, invalidShareParam, testNodeParam)
					invalidShareString, err := buildFenceAgentParams(invalidValTestFAR)
					Expect(err).NotTo(HaveOccurred())
					validShareString, err := buildFenceAgentParams(underTestFAR)
					Expect(err).NotTo(HaveOccurred())
					// Eventually buildFenceAgentParams would return the same shareParam
					Expect(isEqualStringLists(invalidShareString, validShareString)).To(BeTrue())
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
		nodeKey := client.ObjectKey{Name: workerNode}
		farNamespacedName := client.ObjectKey{Name: workerNode, Namespace: defaultNamespace}
		farNoExecuteTaint := utils.CreateFARNoExecuteTaint()
		resourceDeletionWasTriggered := true // corresponds to testVADeletion bool value
		BeforeEach(func() {
			// Create two VAs and two pods, and at the end clean them up with DeferCleanup
			va1 := createVA(vaName1, workerNode)
			va2 := createVA(vaName2, workerNode)
			testPod := createRunningPod("far-test-1", testPodName, workerNode)
			DeferCleanup(verifyResourceCleanup, va1, va2, testPod)
			farPod := createRunningPod("far-manager-test", farPodName, "")
			DeferCleanup(k8sClient.Delete, context.Background(), farPod)
		})
		JustBeforeEach(func() {
			// Create node, and FAR CR, and at the end clean them up with DeferCleanup
			Expect(k8sClient.Create(context.Background(), node)).To(Succeed())
			DeferCleanup(k8sClient.Delete, context.Background(), node)
			Expect(k8sClient.Create(context.Background(), underTestFAR)).To(Succeed())
			DeferCleanup(k8sClient.Delete, context.Background(), underTestFAR)
		})

		// TODO: add more scenarios?
		When("creating valid FAR CR", func() {
			BeforeEach(func() {
				node = utils.GetNode("", workerNode)
			})
			It("should have finalizer, taint, while the two VAs and one pod will be deleted", func() {
				By("Searching for remediation taint")
				Eventually(func() bool {
					Expect(k8sClient.Get(context.Background(), nodeKey, node)).To(Succeed())
					Expect(k8sClient.Get(context.Background(), farNamespacedName, underTestFAR)).To(Succeed())
					res, _ := cliCommandsEquality(underTestFAR)
					return utils.TaintExists(node.Spec.Taints, &farNoExecuteTaint) && res
				}, 100*time.Millisecond, 10*time.Millisecond).Should(BeTrue(), "taint should be added, and command format is correct")

				// If taint was added, then defenintly the finzlier was added as well
				By("Having a finalizer if we have a remediation taint")
				Expect(controllerutil.ContainsFinalizer(underTestFAR, v1alpha1.FARFinalizer)).To(BeTrue())

				By("Not having any VAs nor the test pod")
				testVADeletion(vaName1, resourceDeletionWasTriggered)
				testVADeletion(vaName2, resourceDeletionWasTriggered)
				testPodDeletion(testPodName, resourceDeletionWasTriggered)
			})
		})
		When("creating invalid FAR CR Name", func() {
			BeforeEach(func() {
				node = utils.GetNode("", workerNode)
				// createVA(vaName1, workerNode)
				// createVA(vaName2, workerNode)
				underTestFAR = getFenceAgentsRemediation(dummyNode, fenceAgentIPMI, testShareParam, testNodeParam)
			})
			It("should not have a finalizer nor taint, while the two VAs and one pod will remain", func() {
				By("Not finding a matching node to FAR CR's name")
				nodeKey.Name = dummyNode
				Expect(k8sClient.Get(context.Background(), nodeKey, node)).To(Not(Succeed()))

				By("Not having finalizer")
				farNamespacedName.Name = dummyNode
				Eventually(func() bool {
					Expect(k8sClient.Get(context.Background(), farNamespacedName, underTestFAR)).To(Succeed())
					return controllerutil.ContainsFinalizer(underTestFAR, v1alpha1.FARFinalizer)
				}, 100*time.Millisecond, 10*time.Millisecond).Should(BeFalse(), "finalizer shouldn't be added")

				// If finalizer is missing, then a taint shouldn't be existed
				By("Not having remediation taint")
				Expect(utils.TaintExists(node.Spec.Taints, &farNoExecuteTaint)).To(BeFalse())

				By("Still having all the VAs and one test pod")
				resourceDeletionWasTriggered = false
				testVADeletion(vaName1, resourceDeletionWasTriggered)
				testVADeletion(vaName2, resourceDeletionWasTriggered)
				testPodDeletion(testPodName, resourceDeletionWasTriggered)
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
		},
	}
}

// buildPod builds a dummy pod
func buildPod(containerName, podName, nodeName string) *corev1.Pod {
	pod := &corev1.Pod{}
	pod.Name = podName
	if podName == farPodName {
		// only when we build FAR pod then we add it's label
		pod.Labels = faPodLabels
	} else {
		// testedPod should be reside on unhealthy node
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

// createRunningPod builds new pod format, create it, and set it's status as running
func createRunningPod(containerName, podName, nodeName string) *corev1.Pod {
	pod := buildPod(containerName, podName, nodeName)
	Expect(k8sClient.Create(context.Background(), pod)).To(Succeed())
	pod.Status.Phase = corev1.PodRunning
	Expect(k8sClient.Status().Update(context.Background(), pod)).To(Succeed())
	return pod
}

// createVA creates new volume attachment and return it's object
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

// verifyResourceCleanup fetches all the resources that we have crated for the test
// and if they are still exist at the end of the test, then we clean them up for next test
func verifyResourceCleanup(va1, va2 *storagev1.VolumeAttachment, pod *corev1.Pod) {
	// clean test volume attachments if it exists
	vaTest := &storagev1.VolumeAttachment{}
	if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(va1), vaTest); err == nil {
		log.Info("Cleanup: clean volume attachment", "va name", vaTest.Name)
		Expect(k8sClient.Delete(context.Background(), vaTest)).To(Succeed())
	}
	if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(va2), vaTest); err == nil {
		log.Info("Cleanup: clean volume attachment", "va name", vaTest.Name)
		Expect(k8sClient.Delete(context.Background(), vaTest)).To(Succeed())

	}
	// clean test pod if it exists
	podTest := &corev1.Pod{}
	if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(pod), podTest); err == nil {
		log.Info("Cleanup: clean pod", "pod name", podTest.Name)
		Expect(k8sClient.Delete(context.Background(), podTest)).To(Succeed())
	}
}

// isEqualStringLists return true if two string lists share the same values
func isEqualStringLists(s1, s2 []string) bool {
	sort.Strings(s1)
	sort.Strings(s2)
	return reflect.DeepEqual(s1, s2)
}

// cliCommandsEquality creates the command for CLI and compares it with the production command
func cliCommandsEquality(far *v1alpha1.FenceAgentsRemediation) (bool, error) {
	if mocksExecuter.command == nil {
		return false, errors.New("The command from mocksExecuter is null")
	}

	// hardcode expected command
	//fence_ipmilan --ip=192.168.111.1 --ipport=6233 --username=admin --password=password --action=status --lanplus
	expectedCommand := []string{fenceAgentIPMI, "--lanplus", "--password=password", "--username=admin", "--action=reboot", "--ip=192.168.111.1", "--ipport=6233"}

	fmt.Printf("%s is the command from production environment, and %s is the hardcoded expected command from test environment.\n", mocksExecuter.command, expectedCommand)
	return isEqualStringLists(mocksExecuter.command, expectedCommand), nil
}

// testVADeletion tests whether the volume attachment no longer exist for successful FAR CR
// and consistently check if the volume attachment exist and was not deleted
func testVADeletion(vaName string, resourceDeletionWasTriggered bool) {
	vaKey := client.ObjectKey{
		Namespace: defaultNamespace,
		Name:      vaName,
	}
	if resourceDeletionWasTriggered {
		EventuallyWithOffset(1, func() bool {
			va := &storagev1.VolumeAttachment{}
			err := k8sClient.Get(context.Background(), vaKey, va)
			return apierrors.IsNotFound(err)

		}, 5*time.Second, 250*time.Millisecond).Should(BeTrue())
		log.Info("Volume attachment is no longer exist", "va", vaName)
	} else {
		ConsistentlyWithOffset(1, func() bool {
			va := &storagev1.VolumeAttachment{}
			err := k8sClient.Get(context.Background(), vaKey, va)
			return apierrors.IsNotFound(err)

		}, 5*time.Second, 250*time.Millisecond).Should(BeFalse())
		log.Info("Volume attachment exist", "va", vaName)
	}
}

// testPodDeletion tests whether the pod no longer exist for successful FAR CR
// and consistently check if the pod exist and was not deleted
func testPodDeletion(podName string, resourceDeletionWasTriggered bool) {
	podKey := client.ObjectKey{
		Namespace: defaultNamespace,
		Name:      podName,
	}
	if resourceDeletionWasTriggered {
		EventuallyWithOffset(1, func() bool {
			pod := &corev1.Pod{}
			err := k8sClient.Get(context.Background(), podKey, pod)
			return apierrors.IsNotFound(err)

		}, 5*time.Second, 250*time.Millisecond).Should(BeTrue())
		log.Info("Pod is no longer exist", "pod", podName)
	} else {
		ConsistentlyWithOffset(1, func() bool {
			pod := &corev1.Pod{}
			err := k8sClient.Get(context.Background(), podKey, pod)
			return apierrors.IsNotFound(err)

		}, 5*time.Second, 250*time.Millisecond).Should(BeFalse())
		log.Info("Pod exist", "pod", podName)
	}
}

// Implements Execute function to mock/test Execute of FenceAgentsRemediationReconciler
type mockExecuter struct {
	command []string
	mockLog logr.Logger
}

// newMockExecuter is a dummy function for testing
func newMockExecuter() *mockExecuter {
	mockLogger := ctrl.Log.WithName("mockExecuter")
	mockE := mockExecuter{mockLog: mockLogger}
	return &mockE
}

// Execute is a dummy function for testing which stores the production command
func (m *mockExecuter) Execute(_ *corev1.Pod, command []string) (stdout string, stderr string, err error) {
	m.command = command
	m.mockLog.Info("Executed command has been stored", "command", m.command)
	return SuccessFAResponse + "\n", "", nil
}
