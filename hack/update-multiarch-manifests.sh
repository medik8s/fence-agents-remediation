#!/usr/bin/env bash
# Sync operatorframework.io/arch.* labels (base CSV template) and nodeAffinity
# (manager.yaml) with the PLATFORMS string.
# Usage: ./hack/update-multiarch-manifests.sh "linux/amd64,linux/arm64"
set -euo pipefail

PLATFORMS="${1:-linux/amd64}"

# Parse GOARCH values from "linux/amd64,linux/arm64,..."
arches=()
IFS=',' read -ra parts <<< "$PLATFORMS"
for part in "${parts[@]}"; do
    part="${part// /}"
    [[ "$part" == */* ]] && arches+=("${part##*/}")
done
[[ ${#arches[@]} -eq 0 ]] && arches=("amd64")

# ── Base CSV template: update metadata.labels ────────────────────────────────
CSV_PATH="config/manifests/bases/fence-agents-remediation.clusterserviceversion.yaml"

labels_block="  labels:"$'\n'
for arch in "${arches[@]}"; do
    labels_block+="    operatorframework.io/arch.${arch}: supported"$'\n'
done
labels_block+="    operatorframework.io/os.linux: supported"

awk -v block="$labels_block" '
    /^  labels:$/  { in_labels=1; found=1; print block; next }
    in_labels && /^    / { next }
    in_labels       { in_labels=0 }
    { print }
    END { if (!found) { print "ERROR: metadata.labels block not found in CSV" > "/dev/stderr"; exit 1 } }
' "$CSV_PATH" > "${CSV_PATH}.tmp" && mv "${CSV_PATH}.tmp" "$CSV_PATH"

echo "Updated ${CSV_PATH}: arch.* labels -> ${arches[*]}"

# ── config/manager/manager.yaml: update nodeAffinity values ──────────────────
MGR_PATH="config/manager/manager.yaml"

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
