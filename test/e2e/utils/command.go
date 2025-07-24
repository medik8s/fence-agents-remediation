package utils

// Copy paste from https://github.com/medik8s/node-healthcheck-operator/blob/main/e2e/utils/command.go

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/go-logr/logr"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	"github.com/medik8s/fence-agents-remediation/api/v1alpha1"
)

const (
	containerTestName = "test-command"
	// NOTE: These constants are duplicated from far_e2e_test.go for simplicity.
	fenceAgentAWS  = "fence_aws"
	fenceAgentIPMI = "fence_ipmilan"
)

// Context keys for values needed to run fence commands on the FAR controller pod
const (
	ClientSetContextKey    = "ClientSet"
	ControllerNSContextKey = "ControllerNS"
	LoggerContextKey       = "Logger"
)

// StopKubelet runs cmd command to stop kubelet for the node and returns an error only if it fails
func StopKubelet(c *kubernetes.Clientset, nodeName string, testNsName string, log logr.Logger) error {
	cmd := "microdnf install util-linux -y && /usr/bin/nsenter -m/proc/1/ns/mnt /bin/systemctl stop kubelet"
	_, err := runCommandInCluster(c, nodeName, testNsName, cmd, log)
	if err != nil && strings.Contains(err.Error(), "connection refused") {
		log.Info("ignoring expected error when stopping kubelet", "error", err.Error())
		return nil
	}
	return err
}

// GetBootTime returns the node's boot time, otherwise it fails and returns an error
func GetBootTime(c *kubernetes.Clientset, nodeName string, ns string, log logr.Logger) (time.Time, error) {
	emptyTime := time.Time{}
	output, err := runCommandInCluster(c, nodeName, ns, "microdnf install procps -y >/dev/null 2>&1 && uptime -s", log)
	if err != nil {
		return emptyTime, err
	}

	bootTime, err := time.Parse("2006-01-02 15:04:05", output)
	if err != nil {
		return emptyTime, err
	}

	return bootTime, nil
}

// runCommandInCluster runs a command in a pod in the cluster and returns the output
func runCommandInCluster(c *kubernetes.Clientset, nodeName string, ns string, command string, log logr.Logger) (string, error) {

	// create a pod and wait that it's running
	pod := GetPod(nodeName, containerTestName)
	pod, err := c.CoreV1().Pods(ns).Create(context.Background(), pod, metav1.CreateOptions{})
	if err != nil {
		log.Error(err, "helper pod can't create")
		return "", err
	}

	err = waitForCondition(c, pod, corev1.PodReady, corev1.ConditionTrue, time.Minute)
	if err != nil {
		log.Error(err, "helper pod isn't ready")
		return "", err
	}

	log.Info("helper pod is running, going to execute command")
	cmd := []string{"sh", "-c", command}
	bytes, err := waitForPodOutput(c, pod, cmd)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(bytes)), nil
}

// If the pod has multiple containers (e.g., check with `oc get pod <pod> -o jsonpath='{.spec.containers[*].name}'`),
// specify the target container name explicitly.
func waitForPodOutput(c *kubernetes.Clientset, pod *corev1.Pod, command []string, containerName ...string) ([]byte, error) {
	var out []byte
	if err := wait.PollImmediate(1*time.Second, time.Minute, func() (done bool, err error) {
		out, err = execCommandOnPod(c, pod, command, containerName...)
		if err != nil {
			return false, err
		}

		return len(out) != 0, nil
	}); err != nil {
		return nil, err
	}

	return out, nil
}

// execCommandOnPod runs command in the pod and returns buffer output
func execCommandOnPod(c *kubernetes.Clientset, pod *corev1.Pod, command []string, containerName ...string) ([]byte, error) {
	var outputBuf bytes.Buffer
	var errorBuf bytes.Buffer

	selectedContainer := pod.Spec.Containers[0].Name
	if len(containerName) > 0 {
		selectedContainer = containerName[0]
	}

	req := c.CoreV1().RESTClient().
		Post().
		Namespace(pod.Namespace).
		Resource("pods").
		Name(pod.Name).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: selectedContainer,
			Command:   command,
			Stdin:     true,
			Stdout:    true,
			Stderr:    true,
			TTY:       true,
		}, scheme.ParameterCodec)

	cfg, err := config.GetConfig()
	if err != nil {
		return nil, err
	}

	exec, err := remotecommand.NewSPDYExecutor(cfg, "POST", req.URL())
	if err != nil {
		return nil, err
	}

	err = exec.Stream(remotecommand.StreamOptions{
		Stdin:  os.Stdin,
		Stdout: &outputBuf,
		Stderr: &errorBuf,
		Tty:    true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to run command %v: error: %v, outputStream %s; errorStream %s", command, err, outputBuf.String(), errorBuf.String())
	}

	if errorBuf.Len() != 0 {
		return nil, fmt.Errorf("failed to run command %v: output %s; error %s", command, outputBuf.String(), errorBuf.String())
	}

	return outputBuf.Bytes(), nil
}

// waitForCondition waits until the pod will have specified condition type with the expected status
func waitForCondition(c *kubernetes.Clientset, pod *corev1.Pod, conditionType corev1.PodConditionType, conditionStatus corev1.ConditionStatus, timeout time.Duration) error {
	return wait.PollImmediate(time.Second, timeout, func() (bool, error) {
		updatedPod, err := c.CoreV1().Pods(pod.Namespace).Get(context.TODO(), pod.Name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		for _, c := range updatedPod.Status.Conditions {
			if c.Type == conditionType && c.Status == conditionStatus {
				return true, nil
			}
		}
		return false, nil
	})
}

func GetPod(nodeName, containerName string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "far-test-",
			Labels: map[string]string{
				"test": "",
			},
		},
		Spec: corev1.PodSpec{
			NodeName: nodeName,
			HostPID:  true,
			SecurityContext: &corev1.PodSecurityContext{
				RunAsGroup: pointer.Int64(0),
			},
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:  containerName,
					Image: "registry.access.redhat.com/ubi8/ubi-minimal",
					SecurityContext: &corev1.SecurityContext{
						Privileged: pointer.Bool(true),
						Capabilities: &corev1.Capabilities{
							Drop: []corev1.Capability{"ALL"},
						},
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
					},
					Command: []string{"sleep", "2m"},
				},
			},
			Tolerations: []corev1.Toleration{
				{
					Key:      v1alpha1.FARNoExecuteTaintKey,
					Operator: corev1.TolerationOpExists,
					Effect:   corev1.TaintEffectNoExecute,
				},
				{
					Key:      corev1.TaintNodeOutOfService,
					Operator: corev1.TolerationOpExists,
					Effect:   corev1.TaintEffectNoExecute,
				},
			},
		},
	}
}

// PowerOnNode executes the power-on command to turn on the node during the cleanup phase for the off-action test.
func PowerOnNode(ctx context.Context, far *v1alpha1.FenceAgentsRemediation, targetNodeName string) (bool, error) {
	clientset := ctx.Value(ClientSetContextKey).(*kubernetes.Clientset)
	controllerNS := ctx.Value(ControllerNSContextKey).(string)
	log := ctx.Value(LoggerContextKey).(logr.Logger)

	farController, err := GetActiveFARControllerManagerPod(clientset, controllerNS, log)
	if err != nil {
		return false, err
	}

	cmd, err := CreateFenceCommandForAction(ctx, far, targetNodeName, "on")
	if err != nil {
		log.Error(err, "Unsupported agent")
		return false, err
	}

	bytes, err := waitForPodOutput(clientset, farController, cmd, "manager")

	log.Info("Output of power-on command", "stdout", string(bytes))

	if err != nil {
		log.Error(err, "Power-on Failed")
		return false, err
	}

	log.Info("Power-on command executed successfully", "targetNode", targetNodeName)
	return true, nil
}

func CreateFenceCommandForAction(ctx context.Context, far *v1alpha1.FenceAgentsRemediation, targetNodeName string, action string) ([]string, error) {
	var command []string
	var err error
	var agent string = far.Spec.Agent

	clientset := ctx.Value(ClientSetContextKey).(*kubernetes.Clientset)
	nodeparms := far.Spec.NodeParameters
	sharedparms := far.Spec.SharedParameters

	switch agent {
	case fenceAgentAWS:
		var accessKey, secretKey string
		accessKey, secretKey, err = GetSecretData(clientset, "aws-cloud-fencing-credentials-secret", "openshift-operators", "aws_access_key_id", "aws_secret_access_key")
		command = []string{
			fmt.Sprintf("/usr/sbin/%s", agent),
			fmt.Sprintf("--action=%s", action),
			fmt.Sprintf("--access-key=%s", accessKey),
			fmt.Sprintf("--secret-key=%s", secretKey),
			fmt.Sprintf("--region=%s", sharedparms["--region"]),
			fmt.Sprintf("--plug=%s", nodeparms[v1alpha1.ParameterName("--plug")][v1alpha1.NodeName(targetNodeName)]),
			"--retry-on=10",
		}

	case fenceAgentIPMI:
		var username, password string
		secretBMHName := "ostest-master-0-bmc-secret"
		username, password, err = GetSecretData(clientset, secretBMHName, "openshift-machine-api", "username", "password")
		command = []string{
			fmt.Sprintf("/usr/sbin/%s", agent),
			fmt.Sprintf("--action=%s", action),
			fmt.Sprintf("--username=%s", username),
			fmt.Sprintf("--password=%s", password),
			fmt.Sprintf("--ip=%s", sharedparms["--ip"]),
			fmt.Sprintf("--ipport=%s", nodeparms[v1alpha1.ParameterName("--ipport")][v1alpha1.NodeName(targetNodeName)]),
			"--lanplus",
			"--retry-on=10",
		}
	default:
		err = fmt.Errorf("Unsupported agent: %s", agent)
		return nil, err
	}

	return command, err
}

// GetFARControllerManagerPod returns the first running fence-agents-remediation-controller-manager pod
func GetActiveFARControllerManagerPod(c *kubernetes.Clientset, targetNS string, log logr.Logger) (*corev1.Pod, error) {
	// oc get pods -n OPERATOR_NS -l control-plane=controller-manager -l app.kubernetes.io/name=fence-agents-remediation-operator

	pods, err := c.CoreV1().Pods(targetNS).List(context.Background(), metav1.ListOptions{
		LabelSelector: "control-plane=controller-manager,app.kubernetes.io/name=fence-agents-remediation-operator",
	})

	if err != nil || len(pods.Items) == 0 {
		return nil, fmt.Errorf("FAR controller-manager pod not found")
	}

	var activePod *corev1.Pod
	for _, pod := range pods.Items {
		if pod.Status.Phase == corev1.PodRunning {
			log.Info("Found running FAR controller-manager pod", "name", pod.Name)
			activePod = &pod
			break
		}
	}

	if activePod == nil {
		return nil, fmt.Errorf("no running FAR controller-manager pod found in namespace %s", targetNS)
	}

	return activePod, nil
}
