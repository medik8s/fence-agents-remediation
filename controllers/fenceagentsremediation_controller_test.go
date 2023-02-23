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
	corev1 "k8s.io/api/core/v1"

	"github.com/medik8s/fence-agents-remediation/pkg/cli"
)

var (
	executedCommand []string
)

// Implements Execute function to mock/test Execute of FenceAgentsRemediationReconciler
type mockExecuter struct {
	expected []string
}

// newMockExecuter is a dummy function for testing
func newMockExecuter() cli.Executer {
	mockExpected := []string{"mockExecuter"}
	mockE := mockExecuter{expected: mockExpected}
	return &mockE
}

func (e *mockExecuter) Execute(_ *corev1.Pod, command []string) (stdout string, stderr string, err error) {
	//store the executed command for testing its validaty
	executedCommand = command
	// e.expected = command
	return "", "", nil
}
