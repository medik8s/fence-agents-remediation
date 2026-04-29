package utils

import (
	corev1 "k8s.io/api/core/v1"

	"github.com/medik8s/fence-agents-remediation/api/v1alpha1"
)

// CreateRemediationTaint returns a remediation NoSchedule taint
func CreateRemediationTaint() corev1.Taint {
	return corev1.Taint{
		Key:    v1alpha1.FARNoScheduleTaintKey,
		Effect: corev1.TaintEffectNoSchedule,
	}
}
