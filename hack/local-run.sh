#!/bin/bash
# local-run.sh — Replicate the Kind e2e GitHub Actions workflow locally
#
# Uses local checkouts of FAR and tools instead of cloning from GitHub.
# Assumes standard medik8s directory layout:
#   medik8s/fence-agents-remediation  (this repo)
#   medik8s/tools
#
# fence_kind talks to the container runtime socket mounted into the control-plane
# Kind node. Supports Docker and rootful Podman (auto-detected).
#
# Usage:
#   ./hack/local-run.sh              # Full run (setup + build + deploy + test)
#   ./hack/local-run.sh --skip-setup # Skip cluster creation (reuse existing)
#   ./hack/local-run.sh --skip-build # Skip build and deploy (reuse existing)
#   ./hack/local-run.sh --teardown   # Tear down the cluster

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FAR_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
TOOLS_DIR="${TOOLS_DIR:-${FAR_DIR}/../tools}"
# Only export TOOLS_DIR if it exists; otherwise the Makefile auto-clones tools into .tools.
if [ ! -d "${TOOLS_DIR}" ]; then
    unset TOOLS_DIR
fi

# Auto-detect container tool: prefer podman (rootful), fall back to docker.
# fence_kind talks to the container runtime via the Docker-compatible socket API.
if [ -z "${CONTAINER_TOOL:-}" ]; then
    if command -v podman &>/dev/null; then
        export CONTAINER_TOOL=podman
    elif command -v docker &>/dev/null; then
        export CONTAINER_TOOL=docker
    else
        echo "Error: neither podman nor docker found in PATH."
        exit 1
    fi
fi
export CONTAINER_TOOL

# The Dockerfile is amd64-only (hardcoded Go arch, highavailability repo packages).
# On macOS Apple Silicon, force linux/amd64 so builds use QEMU/Rosetta emulation.
if [[ "$(uname -s)" == "Darwin" && "$(uname -m)" == "arm64" ]]; then
    export DOCKER_BUILD_ARGS="${DOCKER_BUILD_ARGS:---platform linux/amd64}"
fi

# Determine the container runtime socket path for fence_kind.
# The socket is bind-mounted into the control-plane Kind node and used by fence_kind
# inside the manager pod. The mount point inside the pod is always /var/run/docker.sock
# regardless of the host-side path, so test code needs no change.
if [ -z "${CONTAINER_SOCKET_PATH:-}" ]; then
    if [ "${CONTAINER_TOOL}" = "podman" ]; then
        # Query podman for the socket path and whether it is currently active.
        PODMAN_SOCKET_INFO=$(podman info --format '{{.Host.RemoteSocket.Path}} {{.Host.RemoteSocket.Exists}}' 2>/dev/null || true)
        PODMAN_RAW_PATH=$(echo "${PODMAN_SOCKET_INFO}" | awk '{print $1}')
        PODMAN_RUNNING=$(echo "${PODMAN_SOCKET_INFO}" | awk '{print $2}')
        # Strip the unix:// scheme prefix if present.
        CONTAINER_SOCKET_PATH="${PODMAN_RAW_PATH#unix://}"
        if [ -z "${CONTAINER_SOCKET_PATH}" ]; then
            # Fall back to conventional paths when podman info is unavailable.
            if [ "$(id -u)" = "0" ]; then
                CONTAINER_SOCKET_PATH="/run/podman/podman.sock"
            else
                CONTAINER_SOCKET_PATH="/run/user/$(id -u)/podman/podman.sock"
            fi
        fi
        # The Podman API socket must already be running (Podman Desktop / podman machine
        # starts it automatically; on Linux use: systemctl --user start podman.socket).
        if [ "${PODMAN_RUNNING}" != "true" ] && [ ! -S "${CONTAINER_SOCKET_PATH}" ]; then
            echo "Error: Podman socket not active at ${CONTAINER_SOCKET_PATH}."
            echo ""
            echo "  macOS (Podman Desktop or podman machine):  podman machine start"
            echo "  Linux (systemd):  systemctl --user start podman.socket"
            exit 1
        fi
    else
        CONTAINER_SOCKET_PATH="/var/run/docker.sock"
    fi
fi
export CONTAINER_SOCKET_PATH

# --- Configuration (mirrors GitHub Actions env) ---
export MEDIK8S_CLUSTER_NAME="${MEDIK8S_CLUSTER_NAME:-medik8s-ci}"
export MEDIK8S_REGISTRY_NAME="${MEDIK8S_REGISTRY_NAME:-kind-registry}"
export MEDIK8S_REGISTRY_PORT="${MEDIK8S_REGISTRY_PORT:-5000}"
export IMAGE_REGISTRY="${IMAGE_REGISTRY:-${MEDIK8S_REGISTRY_NAME}:${MEDIK8S_REGISTRY_PORT}/medik8s}"
export OPERATOR_NAMESPACE="${OPERATOR_NAMESPACE:-openshift-workload-availability}"
[ -n "${TOOLS_DIR:-}" ] && export TOOLS_DIR

FAR_E2E_BUNDLE="${IMAGE_REGISTRY}/fence-agents-remediation-operator-e2e-bundle:latest"

SKIP_SETUP=false
SKIP_BUILD=false
SKIP_TEST=false
TEARDOWN=false

while [[ $# -gt 0 ]]; do
    case $1 in
        --skip-setup) SKIP_SETUP=true; shift ;;
        --skip-build) SKIP_BUILD=true; shift ;;
        --skip-test)  SKIP_TEST=true; shift ;;
        --teardown)   TEARDOWN=true; shift ;;
        -h|--help)
            echo "Usage: $0 [--skip-setup] [--skip-build] [--skip-test] [--teardown]"
            echo ""
            echo "Replicates the Kind e2e GitHub Actions workflow locally."
            echo ""
            echo "Options:"
            echo "  --skip-setup   Skip cluster creation (reuse existing)"
            echo "  --skip-build   Skip build and deploy (reuse existing images/deployment)"
            echo "  --skip-test    Skip running e2e tests"
            echo "  --teardown     Tear down the cluster and exit"
            echo ""
            echo "Environment variables:"
            echo "  MEDIK8S_CLUSTER_NAME    Kind cluster name (default: medik8s-ci)"
            echo "  CONTAINER_TOOL          Container tool: podman or docker (default: auto-detect)"
            echo "  CONTAINER_SOCKET_PATH   Runtime socket path on host (default: auto-detect)"
            echo "  OPERATOR_NAMESPACE      Operator namespace (default: openshift-workload-availability)"
            echo "  TOOLS_DIR               Path to medik8s/tools checkout (default: ../tools)"
            exit 0
            ;;
        *) echo "Unknown option: $1"; exit 1 ;;
    esac
done

echo "=== Configuration ==="
echo "  Container tool: ${CONTAINER_TOOL}"
echo "  Socket path:    ${CONTAINER_SOCKET_PATH}"
echo ""
echo "=== Local repositories ==="
echo "  FAR:   ${FAR_DIR}"
echo "         branch: $(cd "${FAR_DIR}" && git branch --show-current)"
echo "         commit: $(cd "${FAR_DIR}" && git log --oneline -1)"
if [ -n "${TOOLS_DIR:-}" ] && [ -d "${TOOLS_DIR}" ]; then
    echo "  Tools: ${TOOLS_DIR}"
    echo "         branch: $(cd "${TOOLS_DIR}" && git branch --show-current)"
    echo "         commit: $(cd "${TOOLS_DIR}" && git log --oneline -1)"
else
    echo "  Tools: (will be auto-cloned into .tools by make)"
fi
echo ""

step() {
    echo ""
    echo "========================================"
    echo "  $1"
    echo "========================================"
}

# --- Teardown ---
if [ "${TEARDOWN}" = true ]; then
    step "Tearing down cluster"
    cd "${FAR_DIR}"
    make dev-teardown 2>/dev/null || true
    exit 0
fi

# --- Setup ---
if [ "${SKIP_SETUP}" = false ]; then
    step "Installing operator-sdk"
    cd "${FAR_DIR}"
    make operator-sdk
    # FAR's url-install-tool nests the binary under a version subdirectory.
    OPERATOR_SDK_BIN=$(find ./bin/operator-sdk -name operator-sdk -type f | head -1)
    export PATH="$(dirname "${OPERATOR_SDK_BIN}"):${PATH}"

    step "Creating Kind cluster with registry, OLM, and socket mount"
    cd "${FAR_DIR}"
    SETUP_DOCKER_SOCKET=true \
    KIND_EXPERIMENTAL_PROVIDER="${CONTAINER_TOOL}" \
    make dev-setup

    step "Cluster info"
    cd "${FAR_DIR}"
    make dev-cluster-info
else
    echo "Skipping setup (--skip-setup)"
    OPERATOR_SDK_BIN=$(find "${FAR_DIR}/bin/operator-sdk" -name operator-sdk -type f 2>/dev/null | head -1)
    if [ -n "${OPERATOR_SDK_BIN}" ]; then
        export PATH="$(dirname "${OPERATOR_SDK_BIN}"):${PATH}"
    elif command -v operator-sdk &>/dev/null; then
        : # already on PATH, nothing to do
    else
        step "Installing operator-sdk (not found in bin/ or PATH)"
        cd "${FAR_DIR}"
        make operator-sdk
        OPERATOR_SDK_BIN=$(find ./bin/operator-sdk -name operator-sdk -type f | head -1)
        export PATH="$(dirname "${OPERATOR_SDK_BIN}"):${PATH}"
    fi
fi

# --- Prepare namespace ---
step "Preparing operator namespace"
kubectl create ns "${OPERATOR_NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -
kubectl label --overwrite ns "${OPERATOR_NAMESPACE}" \
    pod-security.kubernetes.io/enforce=privileged \
    pod-security.kubernetes.io/audit=privileged \
    pod-security.kubernetes.io/warn=privileged

# --- Build and deploy ---
if [ "${SKIP_BUILD}" = false ]; then
    step "Building e2e image and OLM bundle"
    cd "${FAR_DIR}"
    make bundle-e2e ${DOCKER_BUILD_ARGS:+DOCKER_BUILD_ARGS="${DOCKER_BUILD_ARGS}"}

    step "Deploying FAR via OLM bundle"
    operator-sdk cleanup fence-agents-remediation -n "${OPERATOR_NAMESPACE}" --timeout 2m 2>/dev/null || true
    operator-sdk run bundle -n "${OPERATOR_NAMESPACE}" --use-http \
        --timeout 5m \
        "${FAR_E2E_BUNDLE}"
else
    echo "Skipping build (--skip-build)"
fi

# --- Wait for operator ---
step "Waiting for operator to be ready"
cd "${FAR_DIR}"
make dev-wait

# --- Pre-load test helper image ---
if [ "${SKIP_TEST}" = false ]; then
    step "Pre-loading test helper image into Kind nodes"
    # runCommandInCluster schedules a ubi8 pod on each worker (GetBootTime, StopKubelet).
    # kind load docker-image injects it into every node's containerd store so pods start instantly.
    ${CONTAINER_TOOL} pull registry.access.redhat.com/ubi8/ubi-minimal
    KIND_EXPERIMENTAL_PROVIDER="${CONTAINER_TOOL}" kind load docker-image \
        registry.access.redhat.com/ubi8/ubi-minimal \
        --name "${MEDIK8S_CLUSTER_NAME}"
fi

# --- Run tests ---
if [ "${SKIP_TEST}" = true ]; then
    echo "Skipping tests (--skip-test)"
else
    step "Running e2e tests"
    cd "${FAR_DIR}"
    E2E_KIND=true \
    OPERATOR_NS="${OPERATOR_NAMESPACE}" \
    make test-e2e || {
        step "Debug (test failed)"
        echo "=== FAR CRs ==="
        kubectl get fenceagentsremediation -A -o yaml 2>/dev/null || true
        echo ""
        echo "=== FART CRs ==="
        kubectl get fenceagentsremediationtemplate -A -o yaml 2>/dev/null || true
        echo ""
        echo "=== CSVs ==="
        kubectl get csv -A 2>/dev/null || true
        echo ""
        echo "=== Subscriptions ==="
        kubectl get sub -A 2>/dev/null || true
        echo ""
        echo "=== Controller logs ==="
        kubectl logs -n "${OPERATOR_NAMESPACE}" -l control-plane=controller-manager --tail=200 2>/dev/null || true
        echo ""
        echo "=== Nodes ==="
        kubectl get nodes -o wide 2>/dev/null || true
        echo ""
        make dev-ci-debug 2>/dev/null || true
        exit 1
    }

    echo ""
    echo "========================================"
    echo "  All tests passed!"
    echo "========================================"
fi
