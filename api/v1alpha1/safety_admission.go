package v1alpha1

import (
	"context"
	"fmt"
	"reflect"

	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

func (v *FARValidator) validateSafetyCreate(ctx context.Context, far *FenceAgentsRemediation) error {
	if err := v.Safety.Validate(); err != nil {
		return err
	}
	if err := ValidateSafetyFAR(far); err != nil {
		return err
	}
	request, err := admission.RequestFromContext(ctx)
	if err != nil || request.UserInfo.Username != v.Safety.TrustedNHCCreator || request.Namespace != far.Namespace {
		return fmt.Errorf("only the configured NHC service account may create safety-mode remediations")
	}
	if v.APIReader == nil {
		return fmt.Errorf("uncached safety admission reader required")
	}
	if _, err := ReadUniqueBoundNode(ctx, v.APIReader, *far.Spec.NodeBinding); err != nil {
		return err
	}
	_, err = ResolveSafetyTarget(ctx, v.APIReader, far, v.Safety)
	return err
}

func (v *FARValidator) validateSafetyUpdate(oldFAR, newFAR *FenceAgentsRemediation) error {
	if err := v.Safety.Validate(); err != nil {
		return err
	}
	if err := ValidateSafetyFAR(newFAR); err != nil {
		return err
	}
	if newFAR.Namespace != v.Safety.CredentialNamespace || !reflect.DeepEqual(oldFAR.Spec, newFAR.Spec) {
		return fmt.Errorf("safety-mode effective spec is immutable")
	}
	return nil
}

func (v *FARTemplateValidator) validateSafetyTemplate(ctx context.Context, tmpl *FenceAgentsRemediationTemplate) error {
	if err := v.Safety.Validate(); err != nil {
		return err
	}
	if err := ValidateSafetySpec(&tmpl.Spec.Template.Spec, true); err != nil {
		return err
	}
	if sample := tmpl.Spec.StatusValidationSample; sample != nil && sample.String() != "0" && sample.String() != "0%" {
		return fmt.Errorf("generic template status execution is unsupported in safety-mode")
	}
	names := GetNodeNamesFromSpec(&tmpl.Spec.Template.Spec)
	if len(names) == 0 {
		names = []string{"template-validation"}
	}
	for _, name := range names {
		far := &FenceAgentsRemediation{}
		far.Namespace = tmpl.Namespace
		far.Spec = *tmpl.Spec.Template.Spec.DeepCopy()
		far.Spec.NodeBinding = &NodeBinding{Version: "v1", Name: name, UID: "template-validation", SystemUUID: "00112233-4455-4677-8899-aabbccddeeff"}
		if _, err := ResolveSafetyTarget(ctx, v.APIReader, far, v.Safety); err != nil {
			return err
		}
	}
	return nil
}
