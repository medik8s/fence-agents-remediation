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
	fenceAgentIPMI = "fence_ipmilan"
)

var (
	faPodLabels    = map[string]string{"app.kubernetes.io/name": "fence-agents-remediation-operator"}
	fenceAgentsPod *corev1.Pod
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
	underTestFAR := getFenceAgentsRemediation(node01, fenceAgentIPMI, testShareParam, testNodeParam)

	Context("Functionality", func() {
		Context("buildFenceAgentParams", func() {
			When("FAR include different action than reboot", func() {
				It("should succeed with a warning", func() {
					invalidValTestFAR := getFenceAgentsRemediation(node01, fenceAgentIPMI, invalidShareParam, testNodeParam)
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
					underTestFAR.ObjectMeta.Name = node01
					Expect(buildFenceAgentParams(underTestFAR)).Error().NotTo(HaveOccurred())
				})
			})
		})
	})
	Context("Reconcile", func() {
		nodeKey := client.ObjectKey{Name: node01}
		farNamespacedName := client.ObjectKey{Name: node01, Namespace: defaultNamespace}
		farNoExecuteTaint := utils.CreateFARNoExecuteTaint()
		//Scenarios
		BeforeEach(func() {
			fenceAgentsPod = buildFarPod()
			// Create, Update status (for GetFenceAgentsRemediationPod), and DeferCleanUp the fenceAgentsPod
			Expect(k8sClient.Create(context.Background(), fenceAgentsPod)).To(Succeed())
			fenceAgentsPod.Status.Phase = corev1.PodRunning
			Expect(k8sClient.Status().Update(context.Background(), fenceAgentsPod)).To(Succeed())
			DeferCleanup(k8sClient.Delete, context.Background(), fenceAgentsPod)
		})
		JustBeforeEach(func() {
			// DeferCleanUp and Create node, and FAR CR
			Expect(k8sClient.Create(context.Background(), node)).To(Succeed())
			DeferCleanup(k8sClient.Delete, context.Background(), node)
			Expect(k8sClient.Create(context.Background(), underTestFAR)).To(Succeed())
			DeferCleanup(k8sClient.Delete, context.Background(), underTestFAR)
		})

		When("creating valid FAR CR", func() {
			BeforeEach(func() {
				node = utils.GetNode("", node01)
			})
			It("should have finalizer and taint", func() {
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

			})
		})
		When("creating invalid FAR CR Name", func() {
			BeforeEach(func() {
				node = utils.GetNode("", node01)
				underTestFAR = getFenceAgentsRemediation(dummyNode, fenceAgentIPMI, testShareParam, testNodeParam)
			})
			It("should not have a finalizer nor taint", func() {
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
