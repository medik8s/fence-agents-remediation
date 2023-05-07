package utils

// Copy paste from https://github.com/medik8s/self-node-remediation/blob/main/e2e/utils/pod.go
import (
	"bytes"
	"context"
	"io"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
)

// GetLogs returns logs of the specified container containerName
func GetLogs(c *kubernetes.Clientset, pod *corev1.Pod, containerName string) (string, error) {
	logStream, err := c.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{Container: containerName}).Stream(context.Background())
	if err != nil {
		return "", err
	}
	defer logStream.Close()

	buf := new(bytes.Buffer)
	if _, err := io.Copy(buf, logStream); err != nil {
		return "", err
	}

	return buf.String(), nil
}
