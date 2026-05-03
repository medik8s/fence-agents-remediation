package taints

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
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
)

const (
	//out of service taint strategy const (supported from 1.26)
	minK8sMajorVersionOutOfServiceTaint           = 1
	minK8sMinorVersionSupportingOutOfServiceTaint = 26

	//out of service taint strategy const (GA from 1.28)
	minK8sMinorVersionGAOutOfServiceTaint = 28
)

// OutOfServiceTaintInfo contains information about out-of-service taint support in the cluster
type OutOfServiceTaintInfo struct {
	Supported bool // true if k8s version >= 1.26
	GA        bool // true if k8s version >= 1.28
}

var (
	loggerTaint   = ctrl.Log.WithName("taints")
	leadingDigits = regexp.MustCompile(`^(\d+)`)
)

// TaintExists checks if the given taint exists in list of taints. Returns true if exists false otherwise.
func TaintExists(taints []corev1.Taint, taintToFind *corev1.Taint) bool {
	for _, taint := range taints {
		if taint.MatchTaint(taintToFind) {
			return true
		}
	}
	return false
}

// Filter removes a taint from the taints slice.
// Since a taint's identity in Kubernetes is uniquely defined by its key and effect,
// this function filters out the unique instance of such a taint matching those fields.
// It returns the updated slice and a boolean indicating if the taint was found and removed.
func Filter(taints []corev1.Taint, taint *corev1.Taint) ([]corev1.Taint, bool) {
	originalLen := len(taints)

	newTaints := slices.DeleteFunc(taints, func(t corev1.Taint) bool {
		return t.MatchTaint(taint)
	})

	return newTaints, len(newTaints) < originalLen
}

// CreateOutOfServiceTaint returns an OutOfService taint.
// TimeAdded is not set - caller should set it when applying the taint to ensure accurate timestamp.
func CreateOutOfServiceTaint() corev1.Taint {
	return corev1.Taint{
		Key:    corev1.TaintNodeOutOfService,
		Value:  "nodeshutdown",
		Effect: corev1.TaintEffectNoExecute,
	}
}

// AddTaintToNode adds the given taint to the node and patches it.
// Returns true if the taint was added, false if it already existed.
// Sets TimeAdded to the current time when adding the taint.
func AddTaintToNode(ctx context.Context, c client.Client, node *corev1.Node, taint corev1.Taint) (bool, error) {
	if TaintExists(node.Spec.Taints, &taint) {
		return false, nil
	}

	patch := client.StrategicMergeFrom(node.DeepCopy())
	now := metav1.Now()
	taint.TimeAdded = &now
	node.Spec.Taints = append(node.Spec.Taints, taint)

	if err := c.Patch(ctx, node, patch); err != nil {
		return false, err
	}
	return true, nil
}

// RemoveTaintFromNode removes the given taint from the node and patches it.
// Returns true if the taint was removed, false if it didn't exist.
func RemoveTaintFromNode(ctx context.Context, c client.Client, node *corev1.Node, taint corev1.Taint) (bool, error) {
	newTaints, removed := Filter(node.Spec.Taints, &taint)
	if !removed {
		return false, nil
	}

	patch := client.StrategicMergeFrom(node.DeepCopy())
	node.Spec.Taints = newTaints

	if err := c.Patch(ctx, node, patch); err != nil {
		return false, err
	}
	return true, nil
}

// DetectOutOfServiceTaintInfoWithRetry detects out-of-service taint support based on k8s version, in case it fails (potentially due to network issues) it will retry for a limited time period.
func DetectOutOfServiceTaintInfoWithRetry(ctx context.Context, config *rest.Config) (OutOfServiceTaintInfo, error) {
	var info OutOfServiceTaintInfo
	var err error
	interval := 2 * time.Second // retry every 2 seconds
	timeout := 10 * time.Second // for a period of 10 seconds

	// Using wait.PollUntilContextTimeout to retry detection in case there is a temporary network issue.
	pollErr := wait.PollUntilContextTimeout(ctx, interval, timeout, true, func(ctx context.Context) (bool, error) {
		if info, err = detectOutOfServiceTaintInfo(config); err != nil {
			return false, nil // Keep retrying
		}
		return true, nil // Success
	})

	// Respect context cancellation - return poll error so caller knows operation was cancelled
	if pollErr != nil && (errors.Is(pollErr, context.Canceled) || errors.Is(pollErr, context.DeadlineExceeded)) {
		return info, pollErr
	}
	// Return info and internal error: nil on success, or last failure on timeout (more specific than generic timeout)
	return info, err
}

func detectOutOfServiceTaintInfo(config *rest.Config) (OutOfServiceTaintInfo, error) {
	var info OutOfServiceTaintInfo
	cs, err := kubernetes.NewForConfig(config)
	if err != nil || cs == nil {
		if err == nil {
			err = fmt.Errorf("k8s client set is nil")
		}
		loggerTaint.Error(err, "couldn't retrieve k8s client")
		return info, err
	}

	k8sVersion, err := cs.Discovery().ServerVersion()
	if err != nil || k8sVersion == nil {
		if err == nil {
			err = fmt.Errorf("k8s server version is nil")
		}
		loggerTaint.Error(err, "couldn't retrieve k8s server version")
		return info, err
	}

	return getOutOfServiceTaintInfo(k8sVersion)
}

func getOutOfServiceTaintInfo(version *version.Info) (OutOfServiceTaintInfo, error) {
	var info OutOfServiceTaintInfo
	var majorVer, minorVer int
	var err error
	if majorVer, err = strconv.Atoi(version.Major); err != nil {
		loggerTaint.Error(err, "couldn't parse k8s major version", "major version", version.Major)
		return info, err
	}
	if minorVer, err = strconv.Atoi(leadingDigits.FindString(version.Minor)); err != nil {
		loggerTaint.Error(err, "couldn't parse k8s minor version", "minor version", version.Minor)
		return info, err
	}

	info.Supported = majorVer > minK8sMajorVersionOutOfServiceTaint || (majorVer == minK8sMajorVersionOutOfServiceTaint && minorVer >= minK8sMinorVersionSupportingOutOfServiceTaint)
	loggerTaint.Info("out of service taint strategy", "isSupported", info.Supported, "k8sMajorVersion", majorVer, "k8sMinorVersion", minorVer)
	info.GA = majorVer > minK8sMajorVersionOutOfServiceTaint || (majorVer == minK8sMajorVersionOutOfServiceTaint && minorVer >= minK8sMinorVersionGAOutOfServiceTaint)
	loggerTaint.Info("out of service taint strategy", "isGA", info.GA, "k8sMajorVersion", majorVer, "k8sMinorVersion", minorVer)
	return info, nil
}
