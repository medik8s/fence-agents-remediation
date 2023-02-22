package cli

import (
	"bytes"
	"errors"

	"github.com/go-logr/logr"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	ctrl "sigs.k8s.io/controller-runtime"
)

type Executer interface {
	Execute(command []string) (stdout string, stderr string, err error)
}

type executer struct {
	Log             logr.Logger
	kClient         *kubernetes.Clientset
	k8sClientConfig *restclient.Config
	containerName   string
	pod             *corev1.Pod
}

// NewExecuter verifies that the pod has running containers and it builds the executer struct
func NewExecuter(pod *corev1.Pod) (Executer, error) {
	logger := ctrl.Log.WithName("controllers").WithName("Executer")
	if len(pod.Spec.Containers) == 0 {
		err := errors.New("create cli executer failed")
		logger.Error(err, "No container found in Pod", "Pod Name", pod.Name)
		return nil, err
	}

	//containerName := pod.Spec.Containers[0].Name
	ce := executer{pod: pod,
		containerName: "manager",
		Log:           logger,
	}
	if err := ce.buildK8sClient(); err != nil {
		return nil, err
	}
	return &ce, nil
}

// buildK8sClient reutrn nil when it successfuly built a Kubernetes client for CLI executer, otherwise an error
func (e *executer) buildK8sClient() error {
	//client was already built, then stop here
	if e.kClient != nil {
		return nil
	}

	if config, err := restclient.InClusterConfig(); err != nil {
		e.Log.Error(err, "failed getting cluster config")
		return err
	} else {
		e.k8sClientConfig = config

		if clientSet, err := kubernetes.NewForConfig(e.k8sClientConfig); err != nil {
			e.Log.Error(err, "failed building k8s client")
			return err
		} else {
			e.kClient = clientSet
		}
	}
	return nil
}

// // IsExecuteable checks whether the Fence Agent is executeable
// func (e *executer) IsExecuteable(command []string) (stdout, stderr string, err error) {

// }

// Execute builds and runs a Post request on contianer for SPDY (shell) executor
func (e *executer) Execute(command []string) (stdout string, stderr string, err error) {
	if err := e.buildK8sClient(); err != nil {
		return "", "", err
	}

	var (
		stdoutBuf bytes.Buffer
		stderrBuf bytes.Buffer
	)

	req := e.kClient.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(e.pod.Name).
		Namespace(e.pod.Namespace).
		SubResource("exec").
		Param("container", e.containerName)

	// Build the Post request for SPDY (shell) executor
	req.VersionedParams(&corev1.PodExecOptions{
		Container: e.containerName,
		Command:   command,
		Stdin:     false,
		Stdout:    true,
		Stderr:    true,
		TTY:       false,
	}, scheme.ParameterCodec)

	// Execute the Post request for SPDY (shell) executor
	execSPDY, err := remotecommand.NewSPDYExecutor(e.k8sClientConfig, "POST", req.URL())
	if err != nil {
		e.Log.Error(err, "failed building SPDY (shell) executor")
		return "", "", err
	}
	err = execSPDY.Stream(remotecommand.StreamOptions{
		Stdout: &stdoutBuf,
		Stderr: &stderrBuf,
		Tty:    false,
	})
	if err != nil {
		e.Log.Error(err, "Failed to run exec command", "command", command, "stdout", stdoutBuf.String(), "stderr", stderrBuf.String())
	} else {
		e.Log.Info("Command has been executed successfully", "command", command, "standard output", stdoutBuf.String())
	}
	return stdoutBuf.String(), stderrBuf.String(), err
}
