package utils

// Copy paste from https://github.com/hybrid-cloud-patterns/patterns-operator/blob/main/controllers/pattern_controller.go#L293-L313

import (
	"context"
	"fmt"

	// "encoding/json"

	"github.com/medik8s/fence-agents-remediation/api/v1alpha1"
	configv1 "github.com/openshift/api/config/v1"
	configclient "github.com/openshift/client-go/config/clientset/versioned"
	machineclient "github.com/openshift/machine-api-operator/pkg/generated/clientset/versioned/typed/machine/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	machineNamespace = "openshift-machine-api"
)

// GetClusterInfo fetch the cluster's infrastructure object to identify it's type
func GetClusterInfo(config configclient.Interface) (*configv1.Infrastructure, error) {
	// oc get Infrastructure.config.openshift.io/cluster

	// clusterList, _ := config.ConfigV1().Infrastructures().List(context.Background(), metav1.ListOptions{})
	// for _, cluster := range clusterList.Items {
	// 	fmt.Printf("\ncluster name:%s and PlatformType: %s \n", string(cluster.Name), string(cluster.Status.PlatformStatus.Type))
	// }

	clusterInfra, err := config.ConfigV1().Infrastructures().Get(context.Background(), "cluster", metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return clusterInfra, nil
}

// GetAWSCredientals searches for AWS secret, and then returns it decoded
func GetCredientals(clientSet *kubernetes.Clientset, secretName, secretKey, secretVal string) (string, string, error) {
	// oc get secrets -n openshift-machine-api aws-cloud-credentials -o jsonpath='{.data.aws_access_key_id}' | base64 -d
	// oc get secrets -n openshift-machine-api aws-cloud-credentials -o jsonpath='{.data.aws_secret_access_key}' | base64 -d

	secret, err := clientSet.CoreV1().Secrets(machineNamespace).Get(context.Background(), secretName, metav1.GetOptions{})
	if err != nil {
		return "", "", err
	}
	fmt.Printf("Key: %s Value: %s \n", secret.Data[secretKey], secret.Data[secretVal])
	// Parse the AWS credentials - AWS access key and secret key
	// var credentials v1beta1credential.OpenShiftCredentialProvider
	// err = json.Unmarshal(awsSecret.Data["credentials"], &credentials)
	// if err != nil {
	// 	return "","", err
	// }
	// return credentials.Spec.SecretRef.KeyValues["access_key_id"], credentials.Spec.SecretRef.KeyValues["secret_access_key"], nil
	// return keyID, keySecret, nil
	return string(secret.Data[secretKey]), string(secret.Data[secretVal]), nil
}

// GetAWSNodeInfoList returns a list of the node names and their identification, e.g., AWS instance ID
func GetAWSNodeInfoList(machineClient *machineclient.MachineV1beta1Client) (map[v1alpha1.NodeName]string, error) {
	//  oc get machine -n openshift-machine-api MACHINE_NAME -o jsonpath='{.spec.providerID}'
	//  oc get machine -n openshift-machine-api MACHINE_NAME -o jsonpath='{.status.nodeRef.name}'

	nodeList := make(map[v1alpha1.NodeName]string)
	// Get the list of Machines in the openshift-machine-api namespace
	machineList, err := machineClient.Machines("openshift-machine-api").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return nodeList, err
	}

	// creates map for nodeName and AWS instance ID
	for _, machine := range machineList.Items {
		nodeName := v1alpha1.NodeName(string(machine.Status.NodeRef.Name))
		fmt.Printf("node: %s Instance ID: %s \n", nodeName, string(*machine.Spec.ProviderID))
		nodeList[nodeName] = string(*machine.Spec.ProviderID)
	}
	return nodeList, nil
}

// GetNodeInfoList returns a list of the node names and their identification, e.g., AWS instance ID
func GetBMNodeInfoList(machineClient *machineclient.MachineV1beta1Client) (map[v1alpha1.NodeName]string, error) {

	//TODO: seacrch for BM and fetch ports

	nodeList := map[v1alpha1.NodeName]string{
		"master-0": "6230",
		"master-1": "6231",
		"master-2": "6232",
		"worker-0": "6233",
		"worker-1": "6234",
		"worker-2": "6235",
	}
	// testNodeParam = map[v1alpha1.ParameterName]map[v1alpha1.NodeName]string{
	// 	"--ipport": {
	// 		"master-0": "6230",
	// 		"master-1": "6231",
	// 		"master-2": "6232",
	// 		"worker-0": "6233",
	// 		"worker-1": "6234",
	// 		"worker-2": "6235",
	// 	},
	// }
	return nodeList, nil
}
