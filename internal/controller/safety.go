package controller

import (
	"context"
	"fmt"
	"reflect"
	"time"

	commonconditions "github.com/medik8s/common/pkg/conditions"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/medik8s/fence-agents-remediation/v5/api/v1alpha1"
	"github.com/medik8s/fence-agents-remediation/v5/pkg/redfish"
	"github.com/medik8s/fence-agents-remediation/v5/pkg/utils"
)

func (r *FenceAgentsRemediationReconciler) reconcileSafety(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if err := r.Safety.Validate(); err != nil {
		return ctrl.Result{}, err
	}
	if r.APIReader == nil {
		return ctrl.Result{}, fmt.Errorf("safety-mode requires an uncached API reader")
	}
	far := &v1alpha1.FenceAgentsRemediation{}
	if err := r.APIReader.Get(ctx, req.NamespacedName, far); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if far.Namespace != r.Safety.CredentialNamespace {
		return ctrl.Result{}, fmt.Errorf("remediation must use the configured BMC namespace")
	}
	if err := v1alpha1.ValidateSafetyFAR(far); err != nil {
		return ctrl.Result{}, err
	}
	if !far.DeletionTimestamp.IsZero() {
		return r.cleanupSafety(ctx, far)
	}
	if isTimedOutByNHC(far) {
		return r.failSafety(ctx, far, fmt.Errorf("remediation stopped by NHC"))
	}
	node, err := v1alpha1.ReadUniqueBoundNode(ctx, r.APIReader, *far.Spec.NodeBinding)
	if err != nil {
		return r.failSafety(ctx, far, err)
	}
	resolved, err := v1alpha1.ResolveSafetyTarget(ctx, r.APIReader, far, r.Safety)
	if err != nil {
		return r.failSafety(ctx, far, err)
	}
	snapshot, err := v1alpha1.NewSafetySnapshot(far, resolved.Target)
	if err != nil {
		return r.failSafety(ctx, far, err)
	}
	if far.Status.SafetySnapshot != nil && *far.Status.SafetySnapshot != snapshot {
		return r.failSafety(ctx, far, fmt.Errorf("bound target, credential version or remediation spec changed"))
	}
	if far.Status.SafetyAttempts < 0 || far.Status.SafetyAttempts > far.Spec.RetryCount || (far.Status.SafetyAttempts > 0 && far.Status.SafetyLastAttempt == nil) {
		return r.failSafety(ctx, far, fmt.Errorf("invalid persisted safety attempt state"))
	}
	if !controllerutil.ContainsFinalizer(far, v1alpha1.FARFinalizer) {
		controllerutil.AddFinalizer(far, v1alpha1.FARFinalizer)
		if err := r.Update(ctx, far); err != nil {
			return ctrl.Result{}, fmt.Errorf("add safety finalizer: %w", err)
		}
		return ctrl.Result{RequeueAfter: time.Millisecond}, nil
	}
	if far.Status.SafetySnapshot == nil {
		far.Status.SafetySnapshot = &snapshot
		far.Status.OffProof = nil
		far.Status.SafetyAttempts = 0
		setSafetyConditions(far, false, false, "TargetBound")
		if err := r.Status().Update(ctx, far); err != nil {
			return ctrl.Result{}, fmt.Errorf("persist selected target: %w", err)
		}
		return ctrl.Result{RequeueAfter: time.Millisecond}, nil
	}
	remediationTaint := utils.CreateRemediationTaint()
	if !utils.TaintExists(node.Spec.Taints, &remediationTaint) {
		node, err = r.checkSafetySnapshot(ctx, far, snapshot)
		if err != nil {
			return r.failSafety(ctx, far, err)
		}
		if err := utils.UpdateBoundTaints(ctx, r.Client, node, snapshot.Binding, []corev1.Taint{remediationTaint}, nil); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Millisecond}, nil
	}
	cfg := redfish.Config{Endpoint: snapshot.Target.Endpoint, SystemURI: snapshot.Target.SystemURI, SystemUUID: snapshot.Binding.SystemUUID, CA: r.Safety.CA, Username: resolved.Username, Password: resolved.Password, Timeout: far.Spec.Timeout.Duration}
	if far.Status.OffProof == nil {
		if _, err := r.checkSafetySnapshot(ctx, far, snapshot); err != nil {
			return r.failSafety(ctx, far, err)
		}
		canAct := far.Status.SafetyAttempts < far.Spec.RetryCount
		if canAct {
			if previous := far.Status.SafetyLastAttempt; previous != nil {
				if delay := time.Until(previous.Add(far.Spec.RetryInterval.Duration)); delay > 0 {
					return ctrl.Result{RequeueAfter: delay}, nil
				}
			}
			far.Status.SafetyAttempts++
			// metav1.Time JSON drops fractions; round up so persistence cannot shorten retry spacing.
			now := metav1.NewTime(time.Now().Truncate(time.Second).Add(time.Second))
			far.Status.SafetyLastAttempt = &now
			if err := r.Status().Update(ctx, far); err != nil {
				return ctrl.Result{}, fmt.Errorf("reserve guarded action attempt: %w", err)
			}
		}
		err := redfish.VerifyOff(ctx, cfg, canAct, func(ctx context.Context) error { _, err := r.checkSafetySnapshot(ctx, far, snapshot); return err })
		if err != nil {
			_, updateErr := r.failSafety(ctx, far, err)
			if far.Status.SafetyAttempts < far.Spec.RetryCount && ctx.Err() == nil {
				if updateErr != nil && updateErr != err {
					return ctrl.Result{}, updateErr
				}
				return ctrl.Result{RequeueAfter: far.Spec.RetryInterval.Duration}, nil
			}
			return ctrl.Result{}, updateErr
		}
		if _, err := r.checkSafetySnapshot(ctx, far, snapshot); err != nil {
			return r.failSafety(ctx, far, err)
		}
		far.Status.OffProof = &v1alpha1.OffProof{Snapshot: snapshot, ConfirmedAt: metav1.Now()}
		setSafetyConditions(far, true, false, "RedfishOffVerified")
		if err := r.Status().Update(ctx, far); err != nil {
			return ctrl.Result{}, fmt.Errorf("persist bound Off proof: %w", err)
		}
		return ctrl.Result{RequeueAfter: time.Millisecond}, nil
	}
	if far.Status.OffProof.Snapshot != snapshot || far.Status.OffProof.ConfirmedAt.IsZero() {
		return r.failSafety(ctx, far, fmt.Errorf("persisted Off proof does not match selected target"))
	}
	offTaint := utils.CreateOutOfServiceTaint()
	if meta.IsStatusConditionTrue(far.Status.Conditions, commonconditions.SucceededType) && utils.TaintExists(node.Spec.Taints, &offTaint) {
		return ctrl.Result{}, nil
	}
	if _, err := r.checkSafetySnapshot(ctx, far, snapshot); err != nil {
		return r.failSafety(ctx, far, err)
	}
	if err := redfish.VerifyOff(ctx, cfg, false, nil); err != nil {
		return r.failSafety(ctx, far, err)
	}
	node, err = r.checkSafetySnapshot(ctx, far, snapshot)
	if err != nil {
		return r.failSafety(ctx, far, err)
	}
	if err := utils.UpdateBoundTaints(ctx, r.Client, node, snapshot.Binding, []corev1.Taint{utils.CreateOutOfServiceTaint()}, nil); err != nil {
		return ctrl.Result{}, err
	}
	setSafetyConditions(far, true, true, "IdentityBoundRelease")
	if err := r.Status().Update(ctx, far); err != nil {
		return ctrl.Result{}, fmt.Errorf("persist identity-bound release: %w", err)
	}
	return ctrl.Result{}, nil
}

func (r *FenceAgentsRemediationReconciler) checkSafetySnapshot(ctx context.Context, original *v1alpha1.FenceAgentsRemediation, expected v1alpha1.SafetySnapshot) (*corev1.Node, error) {
	current := &v1alpha1.FenceAgentsRemediation{}
	if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(original), current); err != nil {
		return nil, fmt.Errorf("read current remediation: %w", err)
	}
	if current.UID != original.UID || !current.DeletionTimestamp.IsZero() || isTimedOutByNHC(current) {
		return nil, fmt.Errorf("remediation identity or lifecycle changed")
	}
	resolved, err := v1alpha1.ResolveSafetyTarget(ctx, r.APIReader, current, r.Safety)
	if err != nil {
		return nil, err
	}
	snapshot, err := v1alpha1.NewSafetySnapshot(current, resolved.Target)
	if err != nil {
		return nil, err
	}
	if snapshot != expected || current.Status.SafetySnapshot == nil || *current.Status.SafetySnapshot != expected {
		return nil, fmt.Errorf("selected target snapshot changed")
	}
	return v1alpha1.ReadUniqueBoundNode(ctx, r.APIReader, expected.Binding)
}

func (r *FenceAgentsRemediationReconciler) failSafety(ctx context.Context, far *v1alpha1.FenceAgentsRemediation, cause error) (ctrl.Result, error) {
	hadProof := far.Status.OffProof != nil
	far.Status.OffProof = nil
	if changed := setSafetyConditions(far, false, false, "SafetyCheckRefused"); changed || hadProof {
		if err := r.Status().Update(ctx, far); err != nil {
			return ctrl.Result{}, fmt.Errorf("persist safety refusal: %w", err)
		}
	}
	return ctrl.Result{}, cause
}

func (r *FenceAgentsRemediationReconciler) cleanupSafety(ctx context.Context, far *v1alpha1.FenceAgentsRemediation) (ctrl.Result, error) {
	binding := *far.Spec.NodeBinding
	if far.Status.SafetySnapshot != nil {
		current, err := v1alpha1.NewSafetySnapshot(far, far.Status.SafetySnapshot.Target)
		if err != nil {
			return ctrl.Result{}, err
		}
		if current != *far.Status.SafetySnapshot {
			return ctrl.Result{}, fmt.Errorf("cleanup binding or effective spec differs from persisted target")
		}
	}
	node := &corev1.Node{}
	err := r.APIReader.Get(ctx, client.ObjectKey{Name: binding.Name}, node)
	if err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("read cleanup Node: %w", err)
	}
	if err == nil && string(node.UID) == binding.UID {
		if err := binding.Matches(node); err != nil {
			return ctrl.Result{}, err
		}
		if err := utils.UpdateBoundTaints(ctx, r.Client, node, binding, nil, []corev1.Taint{utils.CreateOutOfServiceTaint(), utils.CreateRemediationTaint()}); err != nil {
			return ctrl.Result{}, err
		}
	}
	controllerutil.RemoveFinalizer(far, v1alpha1.FARFinalizer)
	if err := r.Update(ctx, far); err != nil {
		return ctrl.Result{}, fmt.Errorf("remove safety finalizer: %w", err)
	}
	return ctrl.Result{}, nil
}

func setSafetyConditions(far *v1alpha1.FenceAgentsRemediation, off, released bool, reason string) bool {
	previous := append([]metav1.Condition(nil), far.Status.Conditions...)
	for _, condition := range []struct {
		kind string
		yes  bool
	}{{commonconditions.ProcessingType, !released && reason != "SafetyCheckRefused"}, {utils.FenceAgentActionSucceededType, off}, {commonconditions.SucceededType, released}} {
		status := metav1.ConditionFalse
		if condition.yes {
			status = metav1.ConditionTrue
		}
		meta.SetStatusCondition(&far.Status.Conditions, metav1.Condition{Type: condition.kind, Status: status, Reason: reason, Message: "Identity-bound Redfish remediation", ObservedGeneration: far.Generation})
	}
	if reflect.DeepEqual(previous, far.Status.Conditions) {
		return false
	}
	now := metav1.Now()
	far.Status.LastUpdateTime = &now
	return true
}
