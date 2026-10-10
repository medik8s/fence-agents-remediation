# AGENTS.md — Fence Agents Remediation Operator

> **IMPORTANT — read this first.** Before making any changes in this repository, you MUST
> read the medik8s **common agent guide**, the **OFFICIAL guidance** for all medik8s
> operators: **https://github.com/medik8s/.github/blob/main/AGENTS.md** . It is
> authoritative project guidance and must not be ignored.

## Medik8s context

FAR is a remediation **provider** in the [medik8s](https://medik8s.io) family. The orchestrator is **Node Healthcheck Operator (NHC)**: it detects unhealthy nodes and creates a `FenceAgentsRemediation` CR; FAR then power-fences the node using a ClusterLabs fence agent and evicts workloads. NHC deletes the CR when the node is healthy again.

FAR is the preferred remediator when direct power management (IPMI, BMC, cloud power API) is available. It provides **hard fencing** with direct confirmation from the management API — no guessing whether the node is truly dead.

## What FAR does

When a `FenceAgentsRemediation` CR is created (CR name = node name):
1. **Taints** the node with a `NoSchedule` remediation taint (marks it unschedulable).
2. **Fences** the node using the configured fence agent (e.g. `fence_ipmilan`, `fence_aws`) with the specified action (`reboot` or `off`). Gets direct confirmation from the agent's API call.
3. **Evicts workloads** from the node to accelerate rescheduling.

Two replicas of FAR run for HA; if the leader is evicted, the other takes over.

## Build & test

```bash
# Unit tests (also runs go-verify, manifests, generate, fmt, vet, imports)
make test-no-verify

# Unit tests + verify no uncommitted changes
make test

# Build operator binary
make manager

# Build container image
make docker-build

# Regenerate CRDs + RBAC after API changes
make manifests generate

# Format + imports
make fmt vet
make fix-imports

# End-to-end tests
make test-e2e
```

> `make test` includes verification of no uncommitted changes. Run `make test-no-verify` during development.

## Local development & testing

Follow the shared workflow in the
[common agent guide](https://github.com/medik8s/.github/blob/main/AGENTS.md) — it documents
the standardized `dev-*` make targets (`dev-setup`, `dev-deploy`, `dev-redeploy`,
`dev-undeploy`, `dev-describe`, `dev-help`, …) provided by `medik8s/tools` (`dev/dev.mk`).

**To develop and test against a real OpenShift / Kubernetes cluster**:
`export SKIP_KIND=true` before the `dev-*` targets; images are pushed to `ttl.sh`.

Github CI workflow runs on a Kind cluster, exercising fencing via `fence_kind` (the e2e
image with `SETUP_DOCKER_SOCKET=true`; the shipped operator image does not include
`fence_kind`). Real power fencing needs IPMI / BMC or a cloud power API, unavailable on
Kind; on a real cluster FAR performs hard fencing via the configured fence agent.

## Key design constraints

- **Fence agents are installed as `fence-agents` RPM packages** (via `dnf`) into the operator image (from the ClusterLabs fence-agents project). When updating the bundled agents, rebuild the container image.
- The `FenceAgentsRemediation` CR name **must match the node name**; if not, FAR ignores it.
- FAR supports `reboot` and `off` actions. `reboot` allows automatic recovery; `off` requires manual intervention. Default is `reboot`.
- Fence agent parameters (e.g. IP, credentials, port) are set per-template and per-CR. Credentials should be stored in Secrets referenced by the CR, not inline.
- FAR runs **two replicas** for HA — ensure RBAC and leader election are preserved when modifying deployment config.
- The admission webhook validates `FenceAgentsRemediationTemplate` — do not bypass it in tests.

## Security

- Operator needs cluster-scoped RBAC to cordon nodes, evict pods, and manage remediation CRs.
- Fence agent credentials (IPMI passwords, cloud keys) must be stored as Kubernetes Secrets.
