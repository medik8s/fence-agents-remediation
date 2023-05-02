package utils

// Copy paste from https://github.com/hybrid-cloud-patterns/patterns-operator/blob/main/controllers/pattern_controller.go#L293-L313

import (
	"context"

	b64 "encoding/base64"
	// "encoding/json"

	"github.com/medik8s/fence-agents-remediation/api/v1alpha1"
	v1 "github.com/openshift/api/config/v1"
	configclient "github.com/openshift/client-go/config/clientset/versioned"
	machineclient "github.com/openshift/machine-api-operator/pkg/generated/clientset/versioned/typed/machine/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	machineNamespace = "openshift-machine-api"
	secretAWS        = "aws-cloud-credentials"
)

// GetClusterInfo fetch the cluster's infrastructure object to identify it's type
func GetClusterInfo(config *configclient.Interface) (*v1.Infrastructure, error) {
	// oc get Infrastructure.config.openshift.io/cluster

	c := *config
	clusterInfra, err := c.ConfigV1().Infrastructures().Get(context.Background(), "cluster", metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return clusterInfra, nil
}

// GetAWSCredientals searches for AWS secret, and then returns it decoded
func GetAWSCredientals(clientSet *kubernetes.Clientset) (string, string, error) {
	// oc get secrets -n openshift-machine-api aws-cloud-credentials -o jsonpath='{.data.aws_access_key_id}' | base64 -d
	// oc get secrets -n openshift-machine-api aws-cloud-credentials -o jsonpath='{.data.aws_secret_access_key}' | base64 -d

	awsSecret, err := clientSet.CoreV1().Secrets(machineNamespace).Get(context.Background(), secretAWS, metav1.GetOptions{})
	if err != nil {
		return "", "", err
	}
	keyID := b64.StdEncoding.EncodeToString([]byte(awsSecret.Data["aws_access_key_id"]))
	keySecret := b64.StdEncoding.EncodeToString([]byte(awsSecret.Data["aws_secret_access_key"]))
	// // Parse the AWS credentials - AWS access key and secret key
	// var credentials v1beta1credential.OpenShiftCredentialProvider
	// err = json.Unmarshal(awsSecret.Data["credentials"], &credentials)
	// if err != nil {
	// 	return "","", err
	// }
	// return credentials.Spec.SecretRef.KeyValues["access_key_id"], credentials.Spec.SecretRef.KeyValues["secret_access_key"], nil
	return keyID, keySecret, nil
}

// GetNodeInfoList returns a list of the node names and their identification, e.g., AWS instance ID
func GetNodeInfoList(machineClient *machineclient.MachineV1beta1Client) (map[v1alpha1.NodeName]string, error) {
	//  oc get machine -n openshift-machine-api MACHINE_NAME -o jsonpath='{.spec.providerID}'
	//  oc get machine -n openshift-machine-api MACHINE_NAME -o jsonpath='{.status.nodeRef.name}'

	var nodeList map[v1alpha1.NodeName]string

	// Get the list of Machines in the openshift-machine-api namespace
	machineList, err := machineClient.Machines("openshift-machine-api").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return nodeList, err
	}

	// creates map for nodeName and AWS instance ID
	for _, machine := range machineList.Items {
		nodeName := v1alpha1.NodeName(string(machine.Status.NodeRef.Name))
		nodeList[nodeName] = string(*machine.Spec.ProviderID)
	}
	return nodeList, nil
}
