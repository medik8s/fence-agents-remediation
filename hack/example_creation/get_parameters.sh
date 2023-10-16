#!/bin/bash
# set -ex
MACHINE_NAMESPACE="openshift-machine-api"

function get_examples_name() {
    SAMPLES_LOCARTION="config/samples/"
    SUFFIX=""
    SUFFIX_FAR_CR_TYPE="FenceAgentsRemediation"
    SUFFIX_FAR_TEMPLATE_CR_TYPE="FenceAgentsRemediationTemplate"
    SUFFIX_FAR_CR="_fence-agents-remediation_v1alpha1_fenceagentsremediation.yaml"
    SUFFIX_FAR_TEMPLATE_CR="_fence-agents-remediation_v1alpha1_fenceagentsremediationtemplate.yaml"
    # [[ $1 == ${SUFFIX_FAR_CR_TYPE} ]] && SUFFIX=${SUFFIX_FAR_CR}
    # [[ $1 == ${SUFFIX_FAR_TEMPLATE_CR_TYPE} ]] && SUFFIX=${SUFFIX_FAR_TEMPLATE_CR}
    if [ "$1" == ${SUFFIX_FAR_CR_TYPE} ]; then
      SUFFIX=${SUFFIX_FAR_CR}
    elif [ "$1" == ${SUFFIX_FAR_TEMPLATE_CR_TYPE} ]; then
      SUFFIX=${SUFFIX_FAR_TEMPLATE_CR}
    fi
    case $2 in
    "AWS")
        # AWS - https://www.mankier.com/8/fence_aws
        echo "${SAMPLES_LOCARTION}"aws"${SUFFIX} far-aws-template"
        ;;
    "BareMetal")
        # BareMetal - https://www.mankier.com/8/fence_ipmilan
        echo "${SAMPLES_LOCARTION}"bmh"${SUFFIX} far-bmh-template"
        ;;
    "Azure")
        # Azure - https://www.mankier.com/8/fence_azure_arm
        echo "${SAMPLES_LOCARTION}"azure"${SUFFIX} far-azure-template"
        ;;
    "GCP")
        # GCP - https://www.mankier.com/8/fence_gce 
        echo "${SAMPLES_LOCARTION}"gcp"${SUFFIX} far-gcp-template"
        ;;
    *)
        echo "\nUnrecognized/unsupported platform- ${platform}\n  " 
        ;;
    esac
}

function get_sharedparameters(){
  # AWS - https://www.mankier.com/8/fence_aws
  SECRET_AWS_NAME="aws-cloud-fencing-credentials-secret"
  SECRET_AWS_NAMESPACE="openshift-operators"

  # BareMetal - https://www.mankier.com/8/fence_ipmilan
  SECRET_BMH_NAME="ostest-master-0-bmc-secret"
  SECRET_BMH_NAMESPACE=${MACHINE_NAMESPACE}

  # Azure - https://www.mankier.com/8/fence_azure_arm
  SECRET_AZURE_NAME="azure-cloud-credentials"
  SECRET_AZURE_NAMESPACE=${MACHINE_NAMESPACE}

  # GCP
  SECRET_GCP_NAME="gcp-cloud-credentials"

  case $1 in
    "AWS")
      # make ocp-aws-credentials
      aws_key_id=$(oc get secrets -n "${SECRET_AWS_NAMESPACE}" "${SECRET_AWS_NAME}" -o jsonpath='{.data.aws_access_key_id}' | base64 -d)
      aws_key=$(oc get secrets -n ${SECRET_AWS_NAMESPACE} ${SECRET_AWS_NAME} -o jsonpath='{.data.aws_secret_access_key}' | base64 -d)
      aws_region=$(oc get machines -o json -n ${MACHINE_NAMESPACE} | jq -r '.items[0].spec.providerID' |  cut -d'/' -f4 | sed 's/.$//')
      
      echo "$aws_key_id $aws_key $aws_region"
      echo "Platform is AWS, thus we are creating fence_aws FA and the secret is" "${SECRET_AWS_NAME}"
      ;;
    "BareMetal")
      bmh_username=$(oc get secrets -n "${SECRET_BMH_NAMESPACE}" "${SECRET_BMH_NAME}" -o jsonpath='{.data.username}' | base64 -d)
      bmh_password=$(oc get secrets -n "${SECRET_BMH_NAMESPACE}" "${SECRET_BMH_NAME}" -o jsonpath='{.data.password}' | base64 -d)

      echo "$bmh_username $bmh_password"
      echo "Platform is BareMetal, thus we are creating fence_ipmilan FA and the secret is" "${SECRET_BMH_NAME}"
      ;;
    "Azure")
      azure_username=$(oc get secrets -n "${SECRET_AZURE_NAMESPACE}" "${SECRET_AZURE_NAME}" -o jsonpath='{.data.azure_client_id}' | base64 -d)
      azure_password=$(oc get secrets -n "${SECRET_AZURE_NAMESPACE}" "${SECRET_AZURE_NAME}" -o jsonpath='{.data.azure_client_secret}' | base64 -d)
      azure_resource_group=$(oc get secrets -n "${SECRET_AZURE_NAMESPACE}" "${SECRET_AZURE_NAME}" -o jsonpath='{.data.azure_resourcegroup}' | base64 -d)
      azure_subscription_id=$(oc get secrets -n "${SECRET_AZURE_NAMESPACE}" "${SECRET_AZURE_NAME}" -o jsonpath='{.data.azure_subscription_id}' | base64 -d)
      azure_tenant_id=$(oc get secrets -n "${SECRET_AZURE_NAMESPACE}" "${SECRET_AZURE_NAME}" -o jsonpath='{.data.azure_tenant_id}' | base64 -d)
      
      echo "$azure_username $azure_password $azure_resource_group $azure_subscription_id $azure_tenant_id "
      echo "Platform is Azure, thus we are creating fence_azure_arm FA and the secret is" "${SECRET_AZURE_NAME}"
      ;;
    "GCP")
      # echo "Platform is GCP, thus we are creating fence_gce FA and the secret is" "${SECRET_GCP_NAME}"
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
      ;;
    *)
      ;;
  esac
}
