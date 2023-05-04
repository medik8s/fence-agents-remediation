package utils

import (
	"fmt"
	"os"
)

// deployNamespaceEnv is a constant for env variable DEPLOYMENT_NAMESPACE
// which specifies the Namespace that the operator's deployment was installed/run.
// It has been set using Downward API (https://kubernetes.io/docs/concepts/workloads/pods/downward-api/) in manager.yaml
const deployNamespaceEnv = "DEPLOYMENT_NAMESPACE"

// GetDeploymentNamespace returns the Namespace this operator is deployed/installed on.
func GetDeploymentNamespace() (string, error) {
	ns, found := os.LookupEnv(deployNamespaceEnv)
	if !found {
		return "", fmt.Errorf("%s must be set", deployNamespaceEnv)
	}
	return ns, nil
}
