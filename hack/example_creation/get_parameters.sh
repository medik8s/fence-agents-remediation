#!/bin/bash
# set -ex
MACHINE_NAMESPACE="openshift-machine-api"
SAMPLES_LOCARTION="config/samples/"

function get_examples_name() {
    SUFFIX=""
    SUFFIX_FAR_CR_TYPE="FenceAgentsRemediation"
    SUFFIX_FAR_TEMPLATE_CR_TYPE="FenceAgentsRemediationTemplate"
    SUFFIX_FAR_CR="_fence-agents-remediation_v1alpha1_fenceagentsremediation.yaml"
    SUFFIX_FAR_TEMPLATE_CR="_fence-agents-remediation_v1alpha1_fenceagentsremediationtemplate.yaml"
    if [ "$1" == ${SUFFIX_FAR_CR_TYPE} ]; then
      SUFFIX=${SUFFIX_FAR_CR}
    elif [ "$1" == ${SUFFIX_FAR_TEMPLATE_CR_TYPE} ]; then
      SUFFIX=${SUFFIX_FAR_TEMPLATE_CR}
    else
      echo "Unrecognized/unsupported-CR-kind error"
      exit 1
    fi

    case $2 in
    "AWS")
        echo "${SAMPLES_LOCARTION}"aws"${SUFFIX} far-aws-template"
        ;;
    "Azure")
        echo "${SAMPLES_LOCARTION}"azure"${SUFFIX} far-azure-template"
        ;;
    "BareMetal")
        echo "${SAMPLES_LOCARTION}"bmh"${SUFFIX} far-bmh-template"
        ;;
    "GCP")
        echo "${SAMPLES_LOCARTION}"gcp"${SUFFIX} far-gcp-template"
        ;;
    *)
        echo "Unrecognized/unsupported-platform-${platform} error" 
        ;;
    esac
}

function get_sharedparameters(){
  SECRET_AWS_NAMESPACE="openshift-operators"
  SECRET_AWS_NAME="aws-cloud-fencing-credentials-secret"
  SECRET_AZURE_NAME="azure-cloud-credentials"
  SECRET_BMH_NAME="ostest-master-0-bmc-secret"
  SECRET_GCP_NAME="gcp-cloud-credentials"
  SERVICE_ACCOUNT_GCP=${SAMPLES_LOCARTION}sa_gcp.json

  case $1 in
    "AWS")
      # make ocp-aws-credentials
      aws_key_id=$(oc get secrets -n "${SECRET_AWS_NAMESPACE}" "${SECRET_AWS_NAME}" -o jsonpath='{.data.aws_access_key_id}' | base64 -d)
      aws_key=$(oc get secrets -n "${SECRET_AWS_NAMESPACE}" "${SECRET_AWS_NAME}" -o jsonpath='{.data.aws_secret_access_key}' | base64 -d)
      aws_region=$(oc get machines -o json -n "${MACHINE_NAMESPACE}" | jq -r '.items[0].spec.providerID' |  cut -d'/' -f4 | sed 's/.$//')
      
      echo "$aws_key_id $aws_key $aws_region"
      echo "Platform is AWS, thus we are creating fence_aws fence agent, and the secret name is" "${SECRET_AWS_NAME}" "at namespace" "${MACHINE_NAMESPACE}" 1>&2
      ;;
    "Azure")
      azure_username=$(oc get secrets -n "${MACHINE_NAMESPACE}" "${SECRET_AZURE_NAME}" -o jsonpath='{.data.azure_client_id}' | base64 -d)
      azure_password=$(oc get secrets -n "${MACHINE_NAMESPACE}" "${SECRET_AZURE_NAME}" -o jsonpath='{.data.azure_client_secret}' | base64 -d)
      azure_resource_group=$(oc get secrets -n "${MACHINE_NAMESPACE}" "${SECRET_AZURE_NAME}" -o jsonpath='{.data.azure_resourcegroup}' | base64 -d)
      azure_subscription_id=$(oc get secrets -n "${MACHINE_NAMESPACE}" "${SECRET_AZURE_NAME}" -o jsonpath='{.data.azure_subscription_id}' | base64 -d)
      azure_tenant_id=$(oc get secrets -n "${MACHINE_NAMESPACE}" "${SECRET_AZURE_NAME}" -o jsonpath='{.data.azure_tenant_id}' | base64 -d)
      
      echo "$azure_username $azure_password $azure_resource_group $azure_subscription_id $azure_tenant_id"
      echo "Platform is Azure, thus we are creating fence_azure_arm fence agent, and the secret name is" "${SECRET_AZURE_NAME}" "at namespace" "${MACHINE_NAMESPACE}" 1>&2
      ;;
    "BareMetal")
      bmh_username=$(oc get secrets -n "${MACHINE_NAMESPACE}" "${SECRET_BMH_NAME}" -o jsonpath='{.data.username}' | base64 -d)
      bmh_password=$(oc get secrets -n "${MACHINE_NAMESPACE}" "${SECRET_BMH_NAME}" -o jsonpath='{.data.password}' | base64 -d)

      echo "$bmh_username $bmh_password"
      echo "Platform is BareMetal, thus we are creating fence_ipmilan fence agent, and the secret name is" "${SECRET_BMH_NAME}" "at namespace" "${MACHINE_NAMESPACE}" 1>&2
      ;;
    "GCP")
      echo "$(oc get secrets -n "${MACHINE_NAMESPACE}" "${SECRET_GCP_NAME}" -o jsonpath='{.data.service_account\.json}' | base64 -d)" > "${SERVICE_ACCOUNT_GCP}"
      gcp_project=$(oc get machines -o json -n "${MACHINE_NAMESPACE}" | jq -r '.items[0].spec.providerID' |  cut -d'/' -f3)
      gcp_zone=$(oc get machines -o json -n "${MACHINE_NAMESPACE}" | jq -r '.items[3].spec.providerID' |  cut -d'/' -f4)
      echo "$SERVICE_ACCOUNT_GCP $gcp_project $gcp_zone"
      echo "Platform is GCP, thus we are creating fence_gce fence agent, and the secret name is" "${SECRET_GCP_NAME}" "at namespace" "${MACHINE_NAMESPACE}" 1>&2
      echo "Attention: The GCP zone might be different per node and by default we select the fourth node. Use the table below" 1>&2
      ;;
    *)
      ;;
  esac
  }
function get_nodeparameters(){
  case $1 in
    "AWS")
      # node -> instance_id
      # Get the instance IDs from the Kubernetes API
      instance_ids=$(oc get machines -o json -n "${MACHINE_NAMESPACE}" | jq -r '.items[].spec.providerID' | awk -F/ '{print $NF}')
      # Create an array by splitting the multiline string on line breaks
      IFS=$'\n' read -r -d '' -a array_instance_ids <<< "${instance_ids}"
      echo "${array_instance_ids[@]}"
      ;;
    "BareMetal")
      # TODO: Find the ports
      # node -> port
      ;;
    "Azure")
      # node_name -> node_name
      ;;
    "GCP")
      # node_name -> node_name
      ;;
    *)
      ;;
  esac
}
