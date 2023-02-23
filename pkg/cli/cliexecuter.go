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
	Execute(pod *corev1.Pod, command []string) (stdout string, stderr string, err error)
}

type executer struct {
	log       logr.Logger
	config    *restclient.Config
	clientSet *kubernetes.Clientset
}

var _ Executer = executer{}

// NewExecuter builds the executer
func NewExecuter(config *restclient.Config) (Executer, error) {
	logger := ctrl.Log.WithName("executer")

	clientSet, err := kubernetes.NewForConfig(config)
	if err != nil {
		logger.Error(err, "failed building k8s client")
		return nil, err
	}

	return &executer{
		log:       logger,
		config:    config,
		clientSet: clientSet,
	}, nil
}

// Execute builds and runs a Post request on contianer for SPDY (shell) executor
func (e executer) Execute(pod *corev1.Pod, command []string) (stdout string, stderr string, err error) {
	if len(pod.Spec.Containers) == 0 {
		err := errors.New("create cli executer failed")
		e.log.Error(err, "No container found in Pod", "Pod Name", pod.Name)
		return "", "", err
	}

	var (
		stdoutBuf bytes.Buffer
		stderrBuf bytes.Buffer
	)

	//containerName := pod.Spec.Containers[0].Name
	containerName := "manager"

	// Build the Post request for SPDY (shell) executor
	req := e.clientSet.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(pod.Name).
		Namespace(pod.Namespace).
		SubResource("exec").
		Param("container", containerName)

	req.VersionedParams(&corev1.PodExecOptions{
		Container: containerName,
		Command:   command,
		Stdin:     false,
		Stdout:    true,
		Stderr:    true,
		TTY:       false,
	}, scheme.ParameterCodec)

	execSPDY, err := remotecommand.NewSPDYExecutor(e.config, "POST", req.URL())
	if err != nil {
		e.log.Error(err, "failed building SPDY (shell) executor")
		return "", "", err
	}

	// Execute the Post request for SPDY (shell) executor
	err = execSPDY.Stream(remotecommand.StreamOptions{
		Stdout: &stdoutBuf,
		Stderr: &stderrBuf,
		Tty:    false,
	})
	if err != nil {
		e.log.Error(err, "Failed to run exec command", "command", command, "stdout", stdoutBuf.String(), "stderr", stderrBuf.String())
	} else {
		e.log.Info("Command has been executed successfully", "command", command, "standard output", stdoutBuf.String())
	}
	return stdoutBuf.String(), stderrBuf.String(), err
}
