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
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/medik8s/fence-agents-remediation/api/v1alpha1"
	"github.com/medik8s/fence-agents-remediation/pkg/cli"
)

const (
	defaultNamespace = "default"
	dummyNodeName    = "dummy-node"
	validNodeName    = "worker-0"
)

var (
	executedCommand []string
)

var _ = Describe("FAR Controller", func() {
	var (
		underTestFAR *v1alpha1.FenceAgentsRemediation
	)

	testFields := []string{"--username", "--password", "--action", "--ip", "--lanplus", "--ipport"}
	testValues := []string{"admin", "password", "reboot", "192.168.111.1", ""}
	nodeFields := []string{"master-0", "master-1", "master-2", "worker-0", "worker-1", "worker-2"}
	nodeValues := []string{"6230", "6231", "6232", "6233", "6234", "6235"}
	testShareParam, testNodeParam := buildFARParameters(testFields, testValues, nodeFields, nodeValues, validNodeName)
	underTestFAR = newFenceAgentsRemediation(validNodeName, " ", testShareParam, testNodeParam)
	fenceAgentsPod := buildFarPod()

	Context("Functionaility", func() {
		When("testing buildFenceAgentParams", func() {
			It("should fail when FAR's name isn't a node name", func() {
				underTestFAR.ObjectMeta.Name = dummyNodeName
				_, err := buildFenceAgentParams(underTestFAR)
				Expect(err).To(HaveOccurred())
			})
			It("should succeed when FAR pod has been created", func() {
				underTestFAR.ObjectMeta.Name = validNodeName
				_, err := buildFenceAgentParams(underTestFAR)
				Expect(err).NotTo(HaveOccurred())
			})
		})
		When("creating a resource", func() {
			It("should fail when FAR pod is missing", func() {
				//Test getFenceAgentsPod func
			})
		})
	})

	Context("Reconcile", func() {
		//Scenarios

		BeforeEach(func() {
			// Create fenceAgentsPod and FAR Template
			Expect(k8sClient.Create(context.Background(), fenceAgentsPod)).NotTo(HaveOccurred())
			Expect(k8sClient.Create(context.Background(), underTestFAR)).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			Expect(k8sClient.Delete(context.Background(), fenceAgentsPod)).NotTo(HaveOccurred())
			Expect(k8sClient.Delete(context.Background(), underTestFAR)).NotTo(HaveOccurred())
		})

		When("creating FAR CR", func() {
			It("should build the exec command based on FAR CR", func() {
				Eventually(func() (bool, error) {
					res, err := cliCommandsEquality(underTestFAR)
					return res, err
				}, 1*time.Second, 500*time.Millisecond).Should(BeTrue(), BeEmpty())
			})
		})
	})
})

// buildFARParameters from string to arrays to two string maps (key-value manner)
func buildFARParameters(fields []string, values []string, nodeFields []string, nodeValues []string, node string) (map[v1alpha1.ParameterName]string, map[v1alpha1.ParameterName]map[v1alpha1.NodeName]string) {
	testShareParam := make(map[v1alpha1.ParameterName]string)
	testNodeParam := make(map[v1alpha1.ParameterName]map[v1alpha1.NodeName]string)
	i := 0
	for i = 0; i < len(values); i++ {
		field := v1alpha1.ParameterName(fields[i])
		testShareParam[field] = values[i]
	}

	nodeName := v1alpha1.NodeName(node)
	numNodeParam := len(fields) - len(values)
	for j := 0; j < numNodeParam; j++ {
		if indexOf(node, nodeFields) > -1 {
			field := v1alpha1.ParameterName(fields[i+j])
			testNodeParam[field] = make(map[v1alpha1.NodeName]string)
			testNodeParam[field][nodeName] = nodeValues[indexOf(node, nodeFields)]
		}
	}
	return testShareParam, testNodeParam
}

// indexOf return the index of element in data array. If it is not found, return -1
func indexOf(element string, data []string) int {
	for i, v := range data {
		if element == v {
			return i
		}
	}
	return -1
}

// newFenceAgentsRemediationTemplate assign the input to the FenceAgentsRemediationTemplate's Spec
func newFenceAgentsRemediation(nodeName string, agent string, sharedparameters map[v1alpha1.ParameterName]string, nodeparameters map[v1alpha1.ParameterName]map[v1alpha1.NodeName]string) *v1alpha1.FenceAgentsRemediation {
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

// cliCommandsEquality creates the command for CLI and compares it with the production command
func cliCommandsEquality(far *v1alpha1.FenceAgentsRemediation) (bool, error) {
	//fence_ipmilan --ip=192.168.111.1 --ipport=6233 --username=admin --password=password --action=status --lanplus
	if executedCommand == nil {
		return false, errors.New("executedCommand is null")
	}
	expectedCommand, err := buildFenceAgentParams(far)
	if err != nil {
		return false, err
	}
	expectedCommand = append([]string{far.Spec.Agent}, expectedCommand...)

	fmt.Printf("%s is the executedCommand in prod, and %s is the expected command in test.\n", executedCommand, expectedCommand)
	sort.Strings(executedCommand)
	sort.Strings(expectedCommand)
	return reflect.DeepEqual(executedCommand, expectedCommand), nil
}

// Implements Execute function to mock/test Execute of FenceAgentsRemediationReconciler
type mockExecuter struct {
	expected []string
	mockLog  logr.Logger
}

// newMockExecuter is a dummy function for testing
func newMockExecuter() cli.Executer {
	mockLogger := ctrl.Log.WithName("mockExecuter")
	mockExpected := []string{"mockExecuter"}
	mockE := mockExecuter{expected: mockExpected, mockLog: mockLogger}
	return &mockE
}

// Execute is a dummy function for testing which stores the production command in the global variable
func (e *mockExecuter) Execute(_ *corev1.Pod, command []string) (stdout string, stderr string, err error) {
	executedCommand = command
	e.expected = command
	e.mockLog.Info("Executed command has been saved", "executedCommand", executedCommand)
	return "", "", nil
}
