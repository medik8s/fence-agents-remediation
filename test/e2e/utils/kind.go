package utils

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// GetKindNodeContainerStatus inspects the Kind node using the test runner's CONTAINER_TOOL.
func GetKindNodeContainerStatus(ctx context.Context, nodeName string) (string, error) {
	containerTool := os.Getenv("CONTAINER_TOOL")
	if containerTool == "" {
		containerTool = "docker"
	}
	output, err := exec.CommandContext(ctx, containerTool, "container", "inspect", "--format", "{{.State.Status}}", nodeName).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("inspect Kind node container %q: %w: %s", nodeName, err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}
