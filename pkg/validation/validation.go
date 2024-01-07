package validation

import (
	"fmt"
	"os"
	"path/filepath"

	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

const ErrorNotFoundAgent = "unsupported fence agent %s:"

func ValidateFenceAgentName(agent string) (admission.Warnings, error) {
	exist, err := isAgentSupported(agent)
	if !exist {
		return nil, fmt.Errorf(ErrorNotFoundAgent+" %w", agent, err)
	}
	return nil, nil

}

// isAgentSupported returns true if the agent name matches a binary, and false otherwise
func isAgentSupported(agent string) (bool, error) {
	directory := "/usr/sbin/"
	// Create the full path by joining the directory and filename
	fullPath := filepath.Join(directory, agent)

	// Check if the file exists
	_, err := os.Stat(fullPath)
	if err == nil {
		fmt.Printf("Agent %s was found at %s\n", agent, directory)
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, fmt.Errorf("agent %s was not found at %s directory", agent, directory)
	}
	return false, fmt.Errorf("error checking file: %v", err)
}
