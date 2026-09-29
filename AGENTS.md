# AGENTS.md — Fence Agents Remediation Operator

## Medik8s context

FAR is a remediation **provider** in the [medik8s](https://medik8s.io) family. The orchestrator is **Node Healthcheck Operator (NHC)**: it detects unhealthy nodes and creates a `FenceAgentsRemediation` CR; FAR then power-fences the node using a ClusterLabs fence agent and evicts workloads. NHC deletes the CR when the node is healthy again. FAR can also be used standalone.

FAR is the preferred remediator when direct power management (IPMI, BMC, cloud power API) is available. It provides **hard fencing** with direct confirmation from the management API — no guessing whether the node is truly dead.

## What FAR does

When a `FenceAgentsRemediation` CR is created (CR name = node name):
1. **Cordons** the node (marks unschedulable).
2. **Fences** the node using the configured fence agent (e.g. `fence_ipmilan`, `fence_aws`) with the specified action (`reboot` or `off`). Gets direct confirmation from the agent's API call.
3. **Evicts workloads** from the node to accelerate rescheduling.

Two replicas of FAR run for HA; if the leader is evicted, the other takes over.

## Repository layout

```
api/v1alpha1/           CRD types: FenceAgentsRemediation, FenceAgentsRemediationTemplate
cmd/                    Manager entrypoint (main.go)
internal/
  controller/           FAR reconciler
  webhook/              Admission webhook
pkg/
  cli/                  Fence agent CLI invocation helpers
  template/             Template reconciler
  utils/                Shared utilities
  validation/           Admission validation logic
test/e2e/               Ginkgo e2e suite
hack/                   Dev scripts
config/                 Kustomize bases (operator, rbac, bundle)
```

## Build & test

```bash
# Unit tests (also runs go-verify, manifests, generate, fmt, vet, imports)
make test-no-verify

# Unit tests + verify no uncommitted changes
make test

# Build operator binary
make build

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

## Local development & deployment

Deploying to a dev cluster is standardized across all medik8s operators via the
shared dev environment in [`medik8s/tools`](https://github.com/medik8s/tools)
(`dev/dev.mk`). The Makefile pulls these targets in automatically: it uses a
sibling `../tools` checkout if present, otherwise shallow-clones the repo into
`.tools/` on first `make dev-*` use.

```bash
make dev-setup       # Create a Kind cluster (1 control-plane + 3 workers) with deps
make dev-deploy      # Build image, load it, install CRDs, deploy the operator
make dev-describe    # Summarize nodes, pods, CRs, leases, and events
make dev-redeploy    # Rebuild and restart pods (fast iteration)
make dev-undeploy    # Remove the operator
make dev-teardown    # Destroy the Kind cluster
make dev-help        # List all dev-* targets
```

Deploy to an existing cluster (OCP, etc.) with `SKIP_KIND=true`; images are
pushed to the ephemeral `ttl.sh` registry:

```bash
export KUBECONFIG=~/.kube/my-cluster
SKIP_KIND=true make dev-setup dev-deploy
```

In Kind there are no real fence agents (no IPMI/BMC or cloud provider APIs), so
what is exercised is controller reconciliation; for full power-fencing use an
external cluster with real BMC/cloud fencing. See
[`dev/README.md`](https://github.com/medik8s/tools/blob/main/dev/README.md) in
`medik8s/tools` for prerequisites, all targets, and per-operator coverage.

## Code style

- Go, Kubebuilder v4, controller-runtime; follows standard medik8s patterns.
- Imports must be sorted (`make fix-imports`).
- No direct commits to `main`; open a PR.

## Key design constraints

- **Fence agents are Python scripts** bundled in the operator image (from ClusterLabs upstream). When adding or updating agents, rebuild the container image.
- The `FenceAgentsRemediation` CR name **must match the node name**; if not, FAR ignores it.
- FAR supports `reboot` and `off` actions. `reboot` allows automatic recovery; `off` requires manual intervention. Default is `reboot`.
- Fence agent parameters (e.g. IP, credentials, port) are set per-template and per-CR. Credentials should be stored in Secrets referenced by the CR, not inline.
- FAR runs **two replicas** for HA — ensure RBAC and leader election are preserved when modifying deployment config.
- The admission webhook validates `FenceAgentsRemediationTemplate` — do not bypass it in tests.

## Security

- Operator needs cluster-scoped RBAC to cordon nodes, evict pods, and manage remediation CRs.
- Fence agent credentials (IPMI passwords, cloud keys) must be stored as Kubernetes Secrets.
- Never widen RBAC beyond the generated `config/rbac/` manifests without review.

## Keeping the docs current

If your changes affect anything described here — build commands, repo layout, fence agent bundling, CR semantics, HA config — or any other existing documentation (`README.md`, `CONTRIBUTING.md`, anything under `docs/`, inline command or usage references), update all of it so the docs never drift from the code.

## Commit conventions

- One-line commit message; sign off with `-s`.
- Reference the relevant issue or PR number when applicable.
- Use WIP in title when creating draft PRs to save CI resources.
