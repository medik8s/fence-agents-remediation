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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/medik8s/fence-agents-remediation/api/v1alpha1"
	"github.com/medik8s/fence-agents-remediation/pkg/utils"
)

const (
	dummyNode      = "dummy-node"
	node01         = "worker-0"
	node02         = "worker-1"
	fenceAgentIPMI = "fence_ipmilan"
)

var (
	faPodLabels    = map[string]string{"app": "fence-agents-remediation-operator"}
	fenceAgentsPod *corev1.Pod
)

var _ = Describe("FAR Controller", func() {
	var (
		underTestFAR *v1alpha1.FenceAgentsRemediation
		node         *corev1.Node
	)

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
	underTestFAR = getFenceAgentsRemediation(node01, fenceAgentIPMI, testShareParam, testNodeParam)

	Context("Functionality", func() {
		Context("buildFenceAgentParams", func() {
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
					underTestFAR.ObjectMeta.Name = node01
					Expect(buildFenceAgentParams(underTestFAR)).Error().NotTo(HaveOccurred())
				})
			})
		})

		Context("IsNodeNameValid", func() {
			BeforeEach(func() {
				node = getNode(node01)
				DeferCleanup(k8sClient.Delete, context.Background(), node)
				Expect(k8sClient.Create(context.Background(), node)).To(Succeed())
			})
			When("FAR CR's name doesn't match to an existing node name", func() {
				It("should fail", func() {
					Expect(utils.IsNodeNameValid(k8sClient, dummyNode)).To(BeFalse())
				})
			})
			When("FAR's name does match to an existing node name", func() {
				It("should succeed", func() {
					Expect(utils.IsNodeNameValid(k8sClient, node01)).To(BeTrue())
				})
			})
		})
	})
	Context("Reconcile", func() {
		//Scenarios
		BeforeEach(func() {
			fenceAgentsPod = buildFarPod()
			// DeferCleanUp and Create fenceAgentsPod
			Expect(k8sClient.Create(context.Background(), fenceAgentsPod)).To(Succeed())
			DeferCleanup(k8sClient.Delete, context.Background(), fenceAgentsPod)
		})
		JustBeforeEach(func() {
			// DeferCleanUp and Create node, and FAR CR
			DeferCleanup(k8sClient.Delete, context.Background(), node)
			DeferCleanup(k8sClient.Delete, context.Background(), underTestFAR)
			Expect(k8sClient.Create(context.Background(), node)).To(Succeed())
			Expect(k8sClient.Create(context.Background(), underTestFAR)).To(Succeed())
		})

		When("creating valid FAR CR", func() {
			BeforeEach(func() {
				node = getNode(node01)
				underTestFAR = getFenceAgentsRemediation(node01, fenceAgentIPMI, testShareParam, testNodeParam)
			})
			It("should have finalizer and taint", func() {
				farNamespacedName := client.ObjectKey{Name: node01, Namespace: defaultNamespace}
				nodeNamespacedName := client.ObjectKey{Name: node01}
				FARNoExecuteTaint := utils.CreateFARNoExecuteTaint()
				Eventually(func() bool {
					Expect(k8sClient.Get(context.Background(), nodeNamespacedName, node)).To(Succeed())
					Expect(k8sClient.Get(context.Background(), farNamespacedName, underTestFAR)).To(Succeed())
					res, _ := cliCommandsEquality(underTestFAR)
					return controllerutil.ContainsFinalizer(underTestFAR, v1alpha1.FARFinalizer) && utils.TaintExists(node.Spec.Taints, &FARNoExecuteTaint) && res
				}, 1*time.Second, 500*time.Millisecond).Should(BeTrue(), "finalizer and taint should be added, and command format is correct")
			})
		})
		When("creating invalid FAR CR Name", func() {
			BeforeEach(func() {
				node = getNode(node01)
				underTestFAR = getFenceAgentsRemediation(dummyNode, fenceAgentIPMI, testShareParam, testNodeParam)
			})
			It("should fail", func() {
				By("Not finding a matching node to FAR CR's name")
				nodeNamespacedName := client.ObjectKey{Name: dummyNode}
				Expect(k8sClient.Get(context.Background(), nodeNamespacedName, node)).To(Not(Succeed()))
				By("Not having finalizer and no taints were added")
				farNamespacedName := client.ObjectKey{Name: dummyNode, Namespace: defaultNamespace}
				FARNoExecuteTaint := utils.CreateFARNoExecuteTaint()
				Eventually(func() bool {
					Expect(k8sClient.Get(context.Background(), farNamespacedName, underTestFAR)).To(Succeed())
					return controllerutil.ContainsFinalizer(underTestFAR, v1alpha1.FARFinalizer) || utils.TaintExists(node.Spec.Taints, &FARNoExecuteTaint)
				}, 1*time.Second, 500*time.Millisecond).Should(BeFalse(), "finalizer shouldn't be added, and node shouldn't be exicted")
			})
		})
	})
})

// newFenceAgentsRemediation assigns the input to the FenceAgentsRemediation
func getFenceAgentsRemediation(nodeName string, agent string, sharedparameters map[v1alpha1.ParameterName]string, nodeparameters map[v1alpha1.ParameterName]map[v1alpha1.NodeName]string) *v1alpha1.FenceAgentsRemediation {
	return &v1alpha1.FenceAgentsRemediation{
		ObjectMeta: metav1.ObjectMeta{Name: nodeName, Namespace: defaultNamespace},
		Spec: v1alpha1.FenceAgentsRemediationSpec{
			Agent:            agent,
			SharedParameters: sharedparameters,
			NodeParameters:   nodeparameters,
		},
	}
}

// used for making new node object for test and have a unique resourceVersion
// getNode returns a node object with the name nodeName
func getNode(nodeName string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: nodeName,
		},
	}
}

// buildFarPod builds a dummy pod with FAR label and namespace
func buildFarPod() *corev1.Pod {
	fenceAgentsPod := &corev1.Pod{}
	fenceAgentsPod.Labels = faPodLabels
	fenceAgentsPod.Name = "mock-fence-agents"
	fenceAgentsPod.Namespace = defaultNamespace
	container := corev1.Container{
		Name:  "foo",
		Image: "foo",
	}
	fenceAgentsPod.Spec.Containers = []corev1.Container{container}
	return fenceAgentsPod
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
	sort.Strings(mocksExecuter.command)
	sort.Strings(expectedCommand)
	return reflect.DeepEqual(mocksExecuter.command, expectedCommand), nil
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
	return SuccessFAResponse, "", nil
}
