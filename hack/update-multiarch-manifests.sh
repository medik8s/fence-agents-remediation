#!/usr/bin/env bash
# Sync operatorframework.io/arch.* labels (base CSV template) and nodeAffinity
# (manager.yaml) with the PLATFORMS string.
# Usage: ./hack/update-multiarch-manifests.sh "linux/amd64,linux/arm64"
set -euo pipefail

PLATFORMS="${1:-linux/amd64}"

CSV_PATH="config/manifests/bases/fence-agents-remediation.clusterserviceversion.yaml"
MGR_PATH="config/manager/manager.yaml"
trap 'rm -f "${CSV_PATH}.tmp" "${MGR_PATH}.tmp"' EXIT

# Parse GOARCH values from "linux/amd64,linux/arm64,..."
arches=()
IFS=',' read -ra parts <<< "$PLATFORMS"
for part in "${parts[@]}"; do
    part="${part// /}"
    [[ -z "$part" ]] && continue
    if [[ "$part" != linux/* || "${part#linux/}" == */* || -z "${part#linux/}" ]]; then
        echo "ERROR: invalid platform '${part}' (expected linux/<arch>)" >&2
        exit 1
    fi
    arches+=("${part#linux/}")
done
[[ ${#arches[@]} -eq 0 ]] && arches=("amd64")

# ── Base CSV template: update metadata.labels ────────────────────────────────
labels_block="  labels:"$'\n'
for arch in "${arches[@]}"; do
    labels_block+="    operatorframework.io/arch.${arch}: supported"$'\n'
done
labels_block+="    operatorframework.io/os.linux: supported"

awk -v block="$labels_block" '
    /^metadata:$/   { in_meta=1 }
    /^[a-z]/ && !/^metadata:$/ { in_meta=0; in_labels=0 }
    in_meta && /^  labels:$/ { in_labels=1; found=1; print block; next }
    in_labels && /^    / { next }
    in_labels        { in_labels=0 }
    { print }
    END { if (!found) { print "ERROR: metadata.labels block not found in CSV" > "/dev/stderr"; exit 1 } }
' "$CSV_PATH" > "${CSV_PATH}.tmp" && mv "${CSV_PATH}.tmp" "$CSV_PATH"

echo "Updated ${CSV_PATH}: arch.* labels -> ${arches[*]}"

# ── config/manager/manager.yaml: update nodeAffinity values ──────────────────
arch_values=""
for arch in "${arches[@]}"; do
    arch_values+="                - ${arch}"$'\n'
done

awk -v av="$arch_values" '
    /key: kubernetes\.io\/arch/ { arch_key=1; found=1; print; next }
    arch_key && /operator: In/  { print; next }
    arch_key && /values:/        { print; printf "%s", av; in_values=1; next }
    in_values && /^                - / { next }
    in_values                    { in_values=0; arch_key=0 }
    { print }
    END { if (!found) { print "ERROR: kubernetes.io/arch affinity block not found in manager.yaml" > "/dev/stderr"; exit 1 } }
' "$MGR_PATH" > "${MGR_PATH}.tmp" && mv "${MGR_PATH}.tmp" "$MGR_PATH"

echo "Updated ${MGR_PATH}: kubernetes.io/arch values -> ${arches[*]}"
