package main

import (
	"fmt"
	"os"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	farapi "github.com/medik8s/fence-agents-remediation/v5/api/v1alpha1"
)

func safetyCacheOptions(config farapi.SafetyConfig) cache.Options {
	if !config.Enabled {
		return cache.Options{}
	}
	objects := map[client.Object]cache.ByObject{}
	for _, object := range []client.Object{&corev1.Secret{}, &farapi.FenceAgentsRemediation{}, &farapi.FenceAgentsRemediationTemplate{}} {
		objects[object] = cache.ByObject{Namespaces: map[string]cache.Config{config.CredentialNamespace: {}}}
	}
	return cache.Options{ByObject: objects}
}

func loadSafetyConfig(enabled bool, creator, namespace, caFile string) (farapi.SafetyConfig, error) {
	config := farapi.SafetyConfig{Enabled: enabled, TrustedNHCCreator: creator, CredentialNamespace: namespace}
	if !enabled {
		return config, nil
	}
	if caFile == "" {
		return config, fmt.Errorf("safety-mode requires a Redfish CA file")
	}
	ca, err := os.ReadFile(caFile)
	if err != nil {
		return config, fmt.Errorf("load Redfish CA: %w", err)
	}
	config.CA = ca
	return config, config.Validate()
}
