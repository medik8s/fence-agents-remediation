package utils

import (
	"context"
	"testing"

	medik8sLabels "github.com/medik8s/common/pkg/labels"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/version"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const node0 = "worker-0"

var _ = Describe("Utils-taint", func() {
	nodeKey := client.ObjectKey{Name: node0}
	controlPlaneRoleTaint := getControlPlaneRoleTaint()
	farNoScheduleTaint := CreateRemediationTaint()
	Context("Taint functioninality test", func() {
		// Check functionaility with control-plane node which already has a taint
		BeforeEach(func() {
			node := GetNode("control-plane", node0)
			Expect(k8sClient.Create(context.Background(), node)).To(Succeed())
			DeferCleanup(k8sClient.Delete, context.Background(), node)
		})
		When("Control-plane node only has 1 the control-plane-role taint", func() {
			It("should add and delete medik8s NoSchedule taint and keep other existing taints", func() {
				By("having one control-plane-role taint")
				taintedNode := &corev1.Node{}
				Expect(k8sClient.Get(context.Background(), nodeKey, taintedNode)).To(Succeed())
				// control-plane-role taint already exist by GetNode
				By("adding medik8s NoSchedule taint")
				Expect(AppendTaint(k8sClient, node0, CreateRemediationTaint())).Error().NotTo(HaveOccurred())
				Expect(k8sClient.Get(context.Background(), nodeKey, taintedNode)).To(Succeed())
				Expect(TaintExists(taintedNode.Spec.Taints, &controlPlaneRoleTaint)).To(BeTrue())
				Expect(TaintExists(taintedNode.Spec.Taints, &farNoScheduleTaint)).To(BeTrue())
				By("removing medik8s NoSchedule taint")
				// We want to see that RemoveTaint only remove the taint it receives
				Expect(RemoveTaint(k8sClient, node0, CreateRemediationTaint())).To(Succeed())
				Expect(k8sClient.Get(context.Background(), nodeKey, taintedNode)).To(Succeed())
				Expect(TaintExists(taintedNode.Spec.Taints, &controlPlaneRoleTaint)).To(BeTrue())
				Expect(TaintExists(taintedNode.Spec.Taints, &farNoScheduleTaint)).To(BeFalse())

				// there is a not-ready taint now as well, so there will be 2 taints... skip count tests
				// Expect(len(taintedNode.Spec.Taints)).To(Equal(1))
			})
		})
	})
})

// getControlPlaneRoleTaint returns a control-plane-role taint
func getControlPlaneRoleTaint() corev1.Taint {
	return corev1.Taint{
		Key:    medik8sLabels.ControlPlaneRole,
		Effect: corev1.TaintEffectNoExecute,
	}
}

func Test_setOutOfTaintFlags(t *testing.T) {
	type args struct {
		version *version.Info
	}
	tests := []struct {
		name                      string
		args                      args
		wantErr                   bool
		isOutOfTaintFlagEnabled   bool
		isOutOfTaintGAFlagEnabled bool
	}{
		//valid use-cases
		{name: "validSupportedButNotGANoPlus", args: args{&version.Info{Major: "1", Minor: "26"}}, wantErr: false, isOutOfTaintFlagEnabled: true, isOutOfTaintGAFlagEnabled: false},
		{name: "validSupportedButNotGAWithPlus", args: args{&version.Info{Major: "1", Minor: "27+"}}, wantErr: false, isOutOfTaintFlagEnabled: true, isOutOfTaintGAFlagEnabled: false},
		{name: "validSupportedAndGANoPlus", args: args{&version.Info{Major: "1", Minor: "28"}}, wantErr: false, isOutOfTaintFlagEnabled: true, isOutOfTaintGAFlagEnabled: true},
		{name: "validSupportedAndGAWithPlus", args: args{&version.Info{Major: "1", Minor: "28+"}}, wantErr: false, isOutOfTaintFlagEnabled: true, isOutOfTaintGAFlagEnabled: true},
		{name: "validSupportedAndGAWithTrailingChars", args: args{&version.Info{Major: "1", Minor: "28.5.2#$%+"}}, wantErr: false, isOutOfTaintFlagEnabled: true, isOutOfTaintGAFlagEnabled: true},
		{name: "validNotSupportedNoPlus", args: args{&version.Info{Major: "1", Minor: "24"}}, wantErr: false, isOutOfTaintFlagEnabled: false, isOutOfTaintGAFlagEnabled: false},
		{name: "validNotSupportedWithPlus", args: args{&version.Info{Major: "1", Minor: "24+"}}, wantErr: false, isOutOfTaintFlagEnabled: false, isOutOfTaintGAFlagEnabled: false},
		{name: "validNotSupportedWithTrailingChars", args: args{&version.Info{Major: "1", Minor: "22.5.2#$%+"}}, wantErr: false, isOutOfTaintFlagEnabled: false, isOutOfTaintGAFlagEnabled: false},

		//invalid use-cases
		{name: "inValidNoPlus", args: args{&version.Info{Major: "1", Minor: "%24"}}, wantErr: true, isOutOfTaintFlagEnabled: false, isOutOfTaintGAFlagEnabled: false},
		{name: "inValidWithPlus", args: args{&version.Info{Major: "1+", Minor: "26"}}, wantErr: true, isOutOfTaintFlagEnabled: false, isOutOfTaintGAFlagEnabled: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			IsOutOfServiceTaintSupported = false
			IsOutOfServiceTaintGA = false
			if err := setOutOfTaintFlags(tt.args.version); (err != nil) != tt.wantErr || IsOutOfServiceTaintSupported != tt.isOutOfTaintFlagEnabled || IsOutOfServiceTaintGA != tt.isOutOfTaintGAFlagEnabled {
				t.Errorf("setOutOfTaintFlags() error = %v, wantErr %v, expected out of taint supported flag %v, expected out of taint GA flag %v",
					err, tt.wantErr, tt.isOutOfTaintFlagEnabled, tt.isOutOfTaintGAFlagEnabled)
			}
		})
	}
}
