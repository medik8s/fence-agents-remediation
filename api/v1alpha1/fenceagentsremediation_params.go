/*
Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	commonAnnotations "github.com/medik8s/common/pkg/annotations"

	corev1 "k8s.io/api/core/v1"
	apiErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilErrors "k8s.io/apimachinery/pkg/util/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/medik8s/fence-agents-remediation/v5/pkg/template"
	"github.com/medik8s/fence-agents-remediation/v5/pkg/validation"
)

const (
	parameterRebootActionValue     = "reboot"
	parameterOffActionValue        = "off"
	ActionName                     = "action"
	ParameterActionName            = "--" + ActionName
	errorParamDefinedMultipleTimes = "invalid multiple definition of FAR parameter, parameter name: %s"
	errorMissingParams             = "invalid spec: mandatory parameters are missing"
	ErrorUnsupportedAction         = "FAR doesn't support any other action than `reboot` or `off`"

	OldDefaultSecretName = "fence-agents-credentials-shared"
)

var (
	// IsOutOfServiceTaintSupported will be set to true in case out-of-service taint is supported (k8s 1.26 or higher)
	IsOutOfServiceTaintSupported bool
	// paramsLog is for logging in this package.
	paramsLog = logf.Log.WithName("fenceagentsremediation-params")
	// verify agent existence with os.Stat function
	agentValidator = validation.NewAgentValidator()
)

func SetOutOfServiceTaintSupported(outOfServiceTaintSupported bool) {
	IsOutOfServiceTaintSupported = outOfServiceTaintSupported
}

// SetAgentValidator sets the agent validator for testing purposes
func SetAgentValidator(validator validation.AgentValidator) {
	agentValidator = validator
}

type SecretParams struct {
	params          map[string]string
	hasNodeTemplate bool
}

// FARValidator implements admission.Validator for FenceAgentsRemediation
// +kubebuilder:object:generate=false
type FARValidator struct {
	Client client.Client
}

var _ admission.Validator[*FenceAgentsRemediation] = &FARValidator{}

func (v *FARValidator) ValidateCreate(ctx context.Context, far *FenceAgentsRemediation) (admission.Warnings, error) {
	return validateSpec(ctx, v.Client, &far.Spec, far)
}

func (v *FARValidator) ValidateUpdate(ctx context.Context, _, newFAR *FenceAgentsRemediation) (admission.Warnings, error) {
	return validateSpec(ctx, v.Client, &newFAR.Spec, newFAR)
}

func (v *FARValidator) ValidateDelete(ctx context.Context, far *FenceAgentsRemediation) (admission.Warnings, error) {
	paramsLog.Info("validate delete", "name", far.GetName())
	return nil, nil
}

// FARTemplateValidator implements admission.Validator for FenceAgentsRemediationTemplate
// +kubebuilder:object:generate=false
type FARTemplateValidator struct {
	Client client.Client
}

var _ admission.Validator[*FenceAgentsRemediationTemplate] = &FARTemplateValidator{}

func (v *FARTemplateValidator) ValidateCreate(ctx context.Context, tmpl *FenceAgentsRemediationTemplate) (admission.Warnings, error) {
	return validateSpec(ctx, v.Client, &tmpl.Spec.Template.Spec, tmpl)
}

func (v *FARTemplateValidator) ValidateUpdate(ctx context.Context, oldTmpl, newTmpl *FenceAgentsRemediationTemplate) (admission.Warnings, error) {
	warnings, err := validateSpec(ctx, v.Client, &newTmpl.Spec.Template.Spec, newTmpl)
	aggregated := utilErrors.NewAggregate([]error{
		err,
		validateTemplateForSharedSecretDefaultName(ctx, v.Client, oldTmpl, newTmpl),
	})
	return warnings, aggregated
}

func (v *FARTemplateValidator) ValidateDelete(ctx context.Context, tmpl *FenceAgentsRemediationTemplate) (admission.Warnings, error) {
	paramsLog.Info("validate delete", "name", tmpl.GetName())
	return nil, nil
}

func validateTemplateForSharedSecretDefaultName(ctx context.Context, k8sClient client.Client, oldTmpl, newTmpl *FenceAgentsRemediationTemplate) error {
	if oldTmpl.Spec.Template.Spec.SharedSecretName == nil ||
		*oldTmpl.Spec.Template.Spec.SharedSecretName != OldDefaultSecretName {
		return nil
	}

	if newTmpl.Spec.Template.Spec.SharedSecretName != nil &&
		*newTmpl.Spec.Template.Spec.SharedSecretName != "" {
		return nil
	}

	secret := &corev1.Secret{}
	secretKey := client.ObjectKey{Name: OldDefaultSecretName, Namespace: newTmpl.Namespace}
	if err := k8sClient.Get(ctx, secretKey, secret); err != nil {
		if apiErrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to check if the default shared secret exists, please retry")
	}
	return fmt.Errorf("shared secret with the deprecated default name %q exists, please delete the secret before removing the name from the FenceAgentsRemediationTemplate CR", OldDefaultSecretName)
}

func validateSpec(ctx context.Context, k8sClient client.Client, spec *FenceAgentsRemediationSpec, metaObj metav1.Object) (admission.Warnings, error) {
	aggregated := utilErrors.NewAggregate([]error{
		validateAgentName(spec.Agent),
		validateStrategy(spec.RemediationStrategy),
		validateTemplateParameters(spec),
		validateFenceAgentForNodes(ctx, k8sClient, metaObj.GetNamespace(), spec),
	})
	return admission.Warnings{}, aggregated
}

func validateAgentName(agent string) error {
	exists, err := agentValidator.ValidateAgentName(agent)
	if err != nil {
		return utilErrors.NewAggregate([]error{
			fmt.Errorf("Failed to validate fence agent: %s. You might want to try again.", agent),
			err,
		})
	}
	if !exists {
		return fmt.Errorf("unsupported fence agent: %s", agent)
	}
	return nil
}

func validateStrategy(farRemStrategy RemediationStrategyType) error {
	if farRemStrategy == OutOfServiceTaintRemediationStrategy && !IsOutOfServiceTaintSupported {
		return fmt.Errorf("%s remediation strategy is not supported at kubernetes version lower than 1.26, please use a different remediation strategy", OutOfServiceTaintRemediationStrategy)
	}
	return nil
}

// validateTemplateParameters validates template syntax in shared parameters and collects all errors
func validateTemplateParameters(spec *FenceAgentsRemediationSpec) error {
	var validationErrors []error

	// Validate NodeTemplate syntax in shared parameters
	for paramName, paramValue := range spec.SharedParameters {
		if _, err := template.RenderParameterTemplate(paramValue, "dummy-node-name"); err != nil {
			validationErrors = append(validationErrors, fmt.Errorf("invalid NodeTemplate syntax in shared parameter %s: %w", paramName, err))
		}
	}

	return utilErrors.NewAggregate(validationErrors)
}

// validateFenceAgentForNodes validates fence agent parameters for all the nodes defined in the spec
// by creating temporary FAR CRs and using BuildFenceAgentParams
func validateFenceAgentForNodes(ctx context.Context, k8sClient client.Client, namespace string, spec *FenceAgentsRemediationSpec) error {

	// Check if spec has any parameters at all
	hasSharedParams := len(spec.SharedParameters) > 0
	hasNodeParams := len(spec.NodeParameters) > 0
	hasSecrets := spec.SharedSecretName != nil || spec.NodeSecretNames != nil

	// If farTemplate has no parameters or secrets, then farTemplate is considered invalid
	if !hasSharedParams && !hasNodeParams && !hasSecrets {
		err := errors.New(errorMissingParams)
		paramsLog.Error(err, "Missing parameters")
		return err
	}

	// Collect all unique node names from NodeParameters and NodeSecretNames
	nodeNames := GetNodeNamesFromSpec(spec)

	// If no node-specific parameters, validate with shared parameters only, use a dummy placeholder for node name
	if len(nodeNames) == 0 {
		paramsLog.Info("validateFenceAgentForNodes no nodes found")
		nodeNames = append(nodeNames, "temp-validation")
	}
	// Validate parameters for each node mentioned in NodeParameters
	for _, nodeName := range nodeNames {
		// Generate a temporary FAR CR from the template for this specific node
		tempFAR := &FenceAgentsRemediation{
			ObjectMeta: metav1.ObjectMeta{
				Name:      nodeName,
				Namespace: namespace,
			},
			Spec: *spec,
		}

		// BuildFenceAgentParams handles secret collection and validation internally
		_, _, err := BuildFenceAgentParams(ctx, k8sClient, tempFAR)
		if err != nil {
			// If BuildFenceAgentParams fails, return the validation error
			return err
		}
	}
	return nil
}

func GetNodeNamesFromSpec(spec *FenceAgentsRemediationSpec) []string {
	nodeNamesMap := make(map[string]bool)
	for _, nodeMap := range spec.NodeParameters {
		for nodeName := range nodeMap {
			nodeNamesMap[string(nodeName)] = true
		}
	}
	for nodeName := range spec.NodeSecretNames {
		nodeNamesMap[string(nodeName)] = true
	}

	return slices.Collect(maps.Keys(nodeNamesMap))
}

// validateFenceAgentParams builds the fence agent parameters map with validation
func validateFenceAgentParams(far *FenceAgentsRemediation, secretParams SecretParams) (map[ParameterName]string, error) {
	nodeName := GetNodeName(far)
	fenceAgentParams := make(map[ParameterName]string)

	isNodeTemplateExistInSharedParams := false
	// Validate and add shared parameters
	for paramName, paramVal := range far.Spec.SharedParameters {
		// Verify action must be reboot or off
		if err := validateFenceAction(string(paramName), paramVal); err != nil {
			return nil, err
		}
		// Verify param isn't already defined
		if _, exist := fenceAgentParams[paramName]; exist {
			err := fmt.Errorf(errorParamDefinedMultipleTimes, paramName)
			paramsLog.Error(err, "can't build fence agents parameters when a parameter is defined multiple times", "parameter name", paramName)
			return nil, err
		}

		processedParamVal, err := template.RenderParameterTemplate(paramVal, nodeName)
		if err != nil {
			paramsLog.Error(err, "Failed to process template in shared parameter", "parameter", paramName, "value", paramVal, "node", nodeName)
			return fenceAgentParams, err
		}
		isNodeTemplateExistInSharedParams = isNodeTemplateExistInSharedParams || paramVal != processedParamVal
		fenceAgentParams[paramName] = processedParamVal
	}

	// Validate and add node parameters (these can override shared parameters)
	for paramName, nodeMap := range far.Spec.NodeParameters {
		if nodeVal, isFound := nodeMap[NodeName(nodeName)]; isFound {
			// Verify action must be reboot or off
			if err := validateFenceAction(string(paramName), nodeVal); err != nil {
				return nil, err
			}
			// For node params we don't enforce uniqueness as node param value will override shared param
			if _, exist := fenceAgentParams[paramName]; exist {
				paramsLog.Info("Shared parameter is overridden by node parameter", "parameter", paramName)
			}
			fenceAgentParams[paramName] = nodeVal
		} else {
			paramsLog.Info("Node parameter is missing for this node", "parameter name", paramName, "node name", nodeName)
		}
	}

	// Validate and add secret parameters
	for secretKey, secretVal := range secretParams.params {
		secretParam := ParameterName(secretKey)
		// Verify action must be reboot or off
		if err := validateFenceAction(string(secretParam), secretVal); err != nil {
			return nil, err
		}
		if _, exist := fenceAgentParams[secretParam]; exist {
			err := fmt.Errorf(errorParamDefinedMultipleTimes, secretParam)
			paramsLog.Error(err, "can't build fence agents parameters when a parameter is defined multiple times", "parameter name", secretParam)
			return nil, err
		}
		fenceAgentParams[secretParam] = secretVal
	}

	onlySharedParamsWithoutTemplate := len(far.Spec.NodeParameters) == 0 && !isNodeTemplateExistInSharedParams && !secretParams.hasNodeTemplate
	if len(fenceAgentParams) == 0 || onlySharedParamsWithoutTemplate {
		err := errors.New(errorMissingParams)
		paramsLog.Error(err, "Missing parameters")
		return nil, err
	}

	return fenceAgentParams, nil
}

// validateFenceAction validates that action parameters are set correctly
func validateFenceAction(paramName, paramVal string) error {
	if (paramName == ActionName || paramName == ParameterActionName) &&
		(paramVal != "" && paramVal != parameterRebootActionValue && paramVal != parameterOffActionValue) {
		// --action parameter with a different value from `reboot` or `off` is not supported
		err := errors.New(ErrorUnsupportedAction)
		paramsLog.Error(err, "can't build CR with this action attribute", "action", paramVal)
		return err
	}
	return nil
}

// BuildFenceAgentParams collects the FAR's parameters for the node based on FAR CR, and if the CR is missing parameters
// or the CR's name don't match nodeParameter name, or it has an action which is different from reboot and off, then return an error
func BuildFenceAgentParams(ctx context.Context, k8sClient client.Client, far *FenceAgentsRemediation) (map[ParameterName]string, bool, error) {
	paramsLog.Info("BuildFenceAgentParams starting", "Node Name", far.Name)

	nodeName := GetNodeName(far)
	secretParams, err := collectAllSecretParams(ctx, k8sClient, far, nodeName)
	if err != nil {
		paramsLog.Error(err, "Failed collecting secrets data", "Node Name", nodeName, "CR Name", far.Name)
		return nil, true, err
	}

	// Build the parameters map with validation included
	fenceAgentParams, err := validateFenceAgentParams(far, secretParams)
	if err != nil {
		return nil, false, err
	}

	// Add the reboot action with its default value - https://github.com/ClusterLabs/fence-agents/blob/main/lib/fencing.py.py#L103
	if _, exist := fenceAgentParams[ParameterActionName]; !exist {
		paramsLog.Info("`action` parameter is missing, so we add it with the default value of `reboot`")
		fenceAgentParams[ParameterActionName] = parameterRebootActionValue
	}

	paramsLog.Info("BuildFenceAgentParams finished successfully ", "Node Name", far.Name)
	return fenceAgentParams, false, nil
}

// GetNodeName checks for the node name in far's commonAnnotations.NodeNameAnnotation if it does not exist it assumes the node name equals to far CR's name and return it.
func GetNodeName(far *FenceAgentsRemediation) string {
	ann := far.GetAnnotations()
	if ann == nil {
		return far.GetName()
	}
	if nodeName, isNodeNameAnnotationExist := ann[commonAnnotations.NodeNameAnnotation]; isNodeNameAnnotationExist {
		return nodeName
	}
	return far.GetName()
}

// collectAllSecretParams collects the parameters from the shared secret and the node secret
func collectAllSecretParams(ctx context.Context, k8sClient client.Client, far *FenceAgentsRemediation, nodeName string) (SecretParams, error) {
	paramsLog.Info("collectAllSecretParams start for node", "node", nodeName)
	secretParams := map[string]string{}
	// Extract secret names and namespace from FAR
	sharedSecretName := far.Spec.SharedSecretName
	nodeSecretNames := far.Spec.NodeSecretNames
	namespace := far.Namespace
	hasNodeTemplate := false

	// collect secret params from shared secret
	if sharedSecretName != nil {
		sharedSecretParams, err := collectSecretParams(ctx, k8sClient, *sharedSecretName, namespace)
		if err != nil {
			return SecretParams{}, err
		}
		// Templating secret shared parameters
		for paramName, paramVal := range sharedSecretParams {
			processedParamVal, err := template.RenderParameterTemplate(paramVal, nodeName)

			if err != nil {
				paramsLog.Error(err, "Failed to process template in shared secret parameter", "parameter", paramName)
				return SecretParams{}, err
			}
			hasNodeTemplate = hasNodeTemplate || processedParamVal != paramVal
			secretParams[paramName] = processedParamVal
		}
	}

	// collect secret params from the node's secret
	nodeSecretName, isFound := nodeSecretNames[NodeName(nodeName)]
	if isFound {
		nodeSecretParams, err := collectSecretParams(ctx, k8sClient, nodeSecretName, namespace)
		if err != nil {
			return SecretParams{}, err
		}
		// Apply node secret params, in case param exist both in shared and node, node param will override the shared.
		maps.Copy(secretParams, nodeSecretParams)
	}
	paramsLog.Info("collectAllSecretParams finish successfully for node", "node", nodeName)
	return SecretParams{secretParams, hasNodeTemplate}, nil
}

// collectSecretParams reads and adds the secret params if they are available
func collectSecretParams(ctx context.Context, k8sClient client.Client, secretName, namespace string) (map[string]string, error) {
	secretParams := make(map[string]string)
	secret := &corev1.Secret{}
	secretKeyObj := client.ObjectKey{Name: secretName, Namespace: namespace}

	if err := k8sClient.Get(ctx, secretKeyObj, secret); err != nil {
		if apiErrors.IsNotFound(err) {
			paramsLog.Error(err, "secret not found", "secret name", secretName, "namespace", namespace)
			return nil, fmt.Errorf("secret '%s' not found in namespace '%s': %w", secretName, namespace, err)
		}
		// For any other error, always return it
		paramsLog.Error(err, "failed to get secret", "secret name", secretName, "namespace", namespace)
		return nil, fmt.Errorf("failed to get secret '%s' in namespace '%s': %w", secretName, namespace, err)
	}

	// fill secret params from secret
	for secretKey, secretVal := range secret.Data {
		secretParams[secretKey] = string(secretVal)
		paramsLog.Info("found a value from secret", "secret name", secretName, "parameter name", secretKey)
	}

	return secretParams, nil
}
