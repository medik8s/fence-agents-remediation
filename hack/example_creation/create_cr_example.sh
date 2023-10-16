#!/bin/bash
# The below script is meant for creating FenceAgentsRemediation/FenceAgentsRemediationTemplate CR for
#  the following platforms: AWS, BareMetal, Azure, and GCP
# Install fence-agents-azure-arm, and fence-agents-gce packages for Azure and GCP fence agents respectively
# set -ex

# Source the functions from get_parameters.sh
source ./hack/example_creation/get_parameters.sh

OPERATOR_NS=$2 #"openshift-operators"
MACHINE_NAMESPACE="openshift-machine-api"
# For template CR we add spaces and spec.template.spec
CR_TEMPLATE_TYPE="FenceAgentsRemediationTemplate"
# Get the node names from the Kubernetes API
nodes=$(oc get machines -o json -n ${MACHINE_NAMESPACE} | jq -r '.items[].status.nodeRef.name')
# Create an array by splitting the multiline string on line breaks
IFS=$'\n' read -r -d '' -a array_nodes <<< "${nodes}"

# Find platform
platform=$(oc get Infrastructure.config.openshift.io/cluster -o jsonpath='{.spec.platformSpec.type}')
example_cr_name=$(get_examples_name $1 "${platform}")
read -r EXAMPLE_NAME CR_NAME <<< "${example_cr_name}"
echo "The script is creating a CR example of kind $1 with at '${EXAMPLE_NAME}'"

# When it is not a remediation template we select the fourth node name to remediate (should be a worker node in case there are only three control-plane nodes)
[[ $1 != ${CR_TEMPLATE_TYPE} ]] && CR_NAME=${array_nodes[3]}

# Generate the FenceAgentsRemediationTemplate CR manifest metadata
cat <<EOF > "${EXAMPLE_NAME}"
apiVersion: fence-agents-remediation.medik8s.io/v1alpha1
kind: $1
metadata:
  name: ${CR_NAME}
  namespace: ${OPERATOR_NS}
spec:
EOF
[[ $1 == ${CR_TEMPLATE_TYPE} ]] && cat <<EOF >> "${EXAMPLE_NAME}"
  template:
    spec:
EOF

nodeparameters=($(get_nodeparameters "${platform}"))
sharedparameters=$(get_sharedparameters "${platform}")

# Generate the FenceAgentsRemediation CR manifest spec
case "${platform}" in
  "AWS")
    cat <<EOF >> "${EXAMPLE_NAME}"
  nodeparameters:    
    '--plug':
EOF
    for i in "${!nodeparameters[@]}"; do
      echo "      ${array_nodes[${i}]}: '${nodeparameters[${i}]}'" >> "${EXAMPLE_NAME}"
    done
    
    # Split the sharedparameters result into individual variables
    read -r aws_key_id aws_key aws_region <<< "${sharedparameters}"
    cat <<EOF >> "${EXAMPLE_NAME}"
  sharedparameters:
    '--region': ${aws_region}
    '--skip-race-check': ''
    '--access-key': ${aws_key_id}
    '--secret-key': ${aws_key}
  agent: fence_aws
EOF
    [[ $1 == ${CR_TEMPLATE_TYPE} ]] && sed -i '9,23 s/^/    /' "${EXAMPLE_NAME}"  
    ;;
  "BareMetal")
    # Split the sharedparameters result into individual variables
    read -r bmh_username bmh_password <<< "${sharedparameters}"
    cat <<EOF >> "${EXAMPLE_NAME}"
  nodeparameters:
    '--ipport':
      master-0: '6230'
      master-1: '6231'
      master-2: '6232'
      worker-0: '6233'
      worker-1: '6234'
      worker-2: '6235'
  sharedparameters:
    '--ip': 192.168.111.1
    '--lanplus': ''
    '--username': ${bmh_username}
    '--password': ${bmh_password}
  agent: fence_ipmilan
EOF
    [[ $1 == ${CR_TEMPLATE_TYPE} ]] && sed -i '9,23 s/^/    /' "${EXAMPLE_NAME}"  
    ;;
  "Azure")
    cat <<EOF >> "${EXAMPLE_NAME}"
  nodeparameters:
    '--plug':
EOF
    for i in "${!array_nodes[@]}"; do
      echo "      ${array_nodes[${i}]}: '${array_nodes[${i}]}'" >> "${EXAMPLE_NAME}"
    done

    # Split the sharedparameters result into individual variables
    read -r azure_username azure_password azure_resource_group azure_subscription_id azure_tenant_id <<< "${sharedparameters}"
    cat <<EOF >> "${EXAMPLE_NAME}"
  sharedparameters:
    '--username': ${azure_username}
    '--password': ${azure_password}
    '--resourceGroup': ${azure_resource_group}
    '--subscriptionId': ${azure_subscription_id}
    '--tenantId': ${azure_tenant_id}
  agent: fence_azure_arm
EOF
    [[ $1 == ${CR_TEMPLATE_TYPE} ]] && sed -i '9,24 s/^/    /' "${EXAMPLE_NAME}"
    ;;
  *)
    ;;
esac

machine=$(oc get machines -o=custom-columns=node:.status.nodeRef.name,machine:.metadata.name,ProviderID:.spec.providerID -n "${MACHINE_NAMESPACE}")
echo "${machine}"