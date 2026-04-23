package utils

// Inspired from SNR - https://github.com/medik8s/self-node-remediation/blob/main/pkg/utils/taints.go
import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/medik8s/fence-agents-remediation/api/v1alpha1"
)

const (
	//out of service taint strategy const (supported from 1.26)
	minK8sMajorVersionOutOfServiceTaint           = 1
	minK8sMinorVersionSupportingOutOfServiceTaint = 26

	//out of service taint strategy const (GA from 1.28)
	minK8sMinorVersionGAOutOfServiceTaint = 28
)

var (
	loggerTaint = ctrl.Log.WithName("taints")
	//IsOutOfServiceTaintSupported will be set to true in case OutOfServiceTaint is supported (k8s 1.26 or higher)
	IsOutOfServiceTaintSupported bool
	//IsOutOfServiceTaintGA will be set to true in case OutOfServiceTaint is GA (k8s 1.28 or higher)
	IsOutOfServiceTaintGA bool
	leadingDigits         = regexp.MustCompile(`^(\d+)`)
)

// Taints are unique by key:effect
// Regardless of the taint's value

// TaintExists checks if the given taint exists in list of taints. Returns true if exists false otherwise.
func TaintExists(taints []corev1.Taint, taintToFind *corev1.Taint) bool {
	for _, taint := range taints {
		if taint.MatchTaint(taintToFind) {
			return true
		}
	}
	return false
}

// deleteTaint removes all the taints that have the same key and effect to given taintToDelete.
func deleteTaint(taints []corev1.Taint, taintToDelete *corev1.Taint) ([]corev1.Taint, bool) {
	var newTaints []corev1.Taint
	deleted := false
	for i := range taints {
		if taintToDelete.MatchTaint(&taints[i]) {
			deleted = true
			continue
		}
		newTaints = append(newTaints, taints[i])
	}
	return newTaints, deleted
}

// CreateRemediationTaint returns a remediation NoSchedule taint
func CreateRemediationTaint() corev1.Taint {
	return corev1.Taint{
		Key:    v1alpha1.FARNoScheduleTaintKey,
		Effect: corev1.TaintEffectNoSchedule,
	}
}

// CreateOutOfServiceTaint returns an OutOfService taint
func CreateOutOfServiceTaint() corev1.Taint {
	now := metav1.Now()
	return corev1.Taint{
		Key:       corev1.TaintNodeOutOfService,
		Value:     "nodeshutdown",
		Effect:    corev1.TaintEffectNoExecute,
		TimeAdded: &now,
	}
}

// AppendTaint appends new taint to the taint list when it is not present.
// It returns bool if a taint was appended, and an error if it fails in the process
func AppendTaint(r client.Client, nodeName string, taint corev1.Taint) (bool, error) {
	// find node by name
	node, err := GetNodeWithName(r, nodeName)
	if node == nil {
		return false, err
	}

	// check if taint doesn't exist
	if TaintExists(node.Spec.Taints, &taint) {
		return false, nil
	}
	// add the taint to the taint list
	now := metav1.Now()
	taint.TimeAdded = &now
	node.Spec.Taints = append(node.Spec.Taints, taint)

	// update with new taint list
	if err := r.Update(context.Background(), node); err != nil {
		loggerTaint.Error(err, "Failed to append taint on node", "node name", node.Name, "taint key", taint.Key, "taint effect", taint.Effect)
		return false, err
	}
	loggerTaint.Info("Taint was added", "taint effect", taint.Effect, "taint list", node.Spec.Taints)
	return true, nil
}

// RemoveTaint removes taint from the taint list when it is existed, and returns error if it fails in the process
func RemoveTaint(r client.Client, nodeName string, taint corev1.Taint) error {
	// find node by name
	node, err := GetNodeWithName(r, nodeName)
	if node == nil {
		return err
	}

	// check if taint exist
	if !TaintExists(node.Spec.Taints, &taint) {
		return nil
	}

	// delete the taint from the taint list
	if taints, deleted := deleteTaint(node.Spec.Taints, &taint); !deleted {
		loggerTaint.Info("Failed to remove taint from node - taint was not found", "node name", node.Name, "taint key", taint.Key, "taint effect", taint.Effect)
		return nil
	} else {
		node.Spec.Taints = taints
	}

	// update with new taint list
	if err := r.Update(context.Background(), node); err != nil {
		return err
	}
	loggerTaint.Info("Taint was removed", "taint effect", taint.Effect, "taint list", node.Spec.Taints)
	return nil
}

// InitOutOfServiceTaintFlagsWithRetry tries to initialize the OutOfService flags based on k8s version, in case it fails (potentially due to network issues) it will retry for a limited number of times
func InitOutOfServiceTaintFlagsWithRetry(ctx context.Context, config *rest.Config) error {

	var err error
	interval := 2 * time.Second // retry every 2 seconds
	timeout := 10 * time.Second // for a period of 10 seconds

	// Since the last internal error returned by InitOutOfServiceTaintFlags also indicates whether polling succeed or not, there is no need to also keep the context error returned by PollUntilContextTimeout.
	// Using wait.PollUntilContextTimeout to retry initOutOfServiceTaintFlags in case there is a temporary network issue.
	_ = wait.PollUntilContextTimeout(ctx, interval, timeout, true, func(ctx context.Context) (bool, error) {
		if err = initOutOfServiceTaintFlags(config); err != nil {
			return false, nil // Keep retrying
		}
		return true, nil // Success
	})
	return err
}

func initOutOfServiceTaintFlags(config *rest.Config) error {
	if cs, err := kubernetes.NewForConfig(config); err != nil || cs == nil {
		if cs == nil {
			err = fmt.Errorf("k8s client set is nil")
		}
		loggerTaint.Error(err, "couldn't retrieve k8s client")
		return err
	} else if k8sVersion, err := cs.Discovery().ServerVersion(); err != nil || k8sVersion == nil {
		if k8sVersion == nil {
			err = fmt.Errorf("k8s server version is nil")
		}
		loggerTaint.Error(err, "couldn't retrieve k8s server version")
		return err
	} else {
		return setOutOfTaintFlags(k8sVersion)
	}
}

func setOutOfTaintFlags(version *version.Info) error {
	var majorVer, minorVer int
	var err error
	if majorVer, err = strconv.Atoi(version.Major); err != nil {
		loggerTaint.Error(err, "couldn't parse k8s major version", "major version", version.Major)
		return err
	}
	if minorVer, err = strconv.Atoi(leadingDigits.FindString(version.Minor)); err != nil {
		loggerTaint.Error(err, "couldn't parse k8s minor version", "minor version", version.Minor)
		return err
	}

	IsOutOfServiceTaintSupported = majorVer > minK8sMajorVersionOutOfServiceTaint || (majorVer == minK8sMajorVersionOutOfServiceTaint && minorVer >= minK8sMinorVersionSupportingOutOfServiceTaint)
	loggerTaint.Info("out of service taint strategy", "isSupported", IsOutOfServiceTaintSupported, "k8sMajorVersion", majorVer, "k8sMinorVersion", minorVer)
	IsOutOfServiceTaintGA = majorVer > minK8sMajorVersionOutOfServiceTaint || (majorVer == minK8sMajorVersionOutOfServiceTaint && minorVer >= minK8sMinorVersionGAOutOfServiceTaint)
	loggerTaint.Info("out of service taint strategy", "isGA", IsOutOfServiceTaintGA, "k8sMajorVersion", majorVer, "k8sMinorVersion", minorVer)
	return nil
}
