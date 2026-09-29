# Fence Agents Remediation (FAR) — Code map

**Repository:** `github.com/medik8s/fence-agents-remediation`

## Purpose

A **reader’s map** of the FAR codebase: where the main ideas live so you can jump from verified memory into the right files. It is **not** a line-by-line tour.

## Top-level layout

```
fence-agents-remediation/
├── cmd/
│   └── main.go                 # Manager: metrics, health, webhooks, OOS version probe, Executer, reconcilers
├── api/v1alpha1/           # CRD types, BuildFenceAgentParams, admission validators (FARValidator/FARTemplateValidator), deepcopy
├── internal/
│   ├── controller/         # FAR reconciler; FenceAgentsRemediationTemplate reconciler (status validation)
│   └── webhook/v1alpha1/   # Webhook manager wiring + defaulters (legacy shared-secret name); validators live in api/v1alpha1
├── pkg/
│   ├── cli/                # Executer: exec fence agents, async UID map, retries, status updates
│   ├── utils/              # Conditions, events, taints, node lookup; pods/namespaces helpers
│   ├── template/           # {{.NodeName}} parameter rendering
│   └── validation/         # Agent binary under /usr/sbin/; K8s version for out-of-service support
├── version/                # Build metadata (printed at startup)
├── config/                 # CRD bases, kustomize, webhook manifests
├── bundle/                 # OLM bundle
└── vendor/                 # Third-party (read-only for navigation)
```

## Where to look for what

| Question | Start here |
|----------|------------|
| How is a **FenceAgentsRemediation** reconciled end to end? | `internal/controller/fenceagentsremediation_controller.go` (includes **`handleFARDeletion`**) |
| How is the **fence CLI** argv built (secrets, merge, default **action**)? | `api/v1alpha1/fenceagentsremediation_params.go` |
| **Webhooks** / defaulters / legacy **shared secret** workaround? | `internal/webhook/v1alpha1/fenceagentsremediation_webhook.go`, `fenceagentsremediationtemplate_webhook.go` (manager wiring + defaulters; validators (`FARValidator`/`FARTemplateValidator`) live in `api/v1alpha1/fenceagentsremediation_params.go`) |
| Template **status** validation (**sample**, **`--action=status`**, timeout)? | `internal/controller/fenceagentsremediationtemplate_controller.go` |
| How does the **subprocess** run (retries, timeout, condition updates)? | `pkg/cli/cliexecuter.go` |
| **Processing** / **Succeeded** / **FenceAgentActionSucceeded** updates? | `pkg/utils/conditions.go` |
| **Event** reason constants? | `pkg/utils/events.go` |
| **Taints** (FAR NoSchedule, out-of-service)? | `pkg/utils/taints.go` |
| **Node** lookup (NotFound → nil)? | `pkg/utils/nodes.go` |
| **`{{.NodeName}}` templating**? | `pkg/template/template.go` |
| **Out-of-service** supported on this cluster? | `pkg/validation/validation.go` (**`NewOutOfServiceTaintValidator`** + **`IsOutOfServiceTaintSupported()`**) wired in `cmd/main.go`, flag pushed into `api/v1alpha1` via **`SetOutOfServiceTaintSupported`** |

### `pkg/utils/pods.go` and `namespaces.go`

Helpers to find the **operator pod** / **deployment namespace**—primarily useful for **tests** or auxiliary tooling; core reconcile paths do not require them for FAR remediation logic.

## Tests and generated code

**`*_test.go`** and **`zz_generated.deepcopy.go`** matter for correctness and API churn; for **runtime** behaviour, start with **`internal/controller/*.go`** and **`api/v1alpha1/*.go`** excluding tests.

## API short names (CLI)

| Resource | Short names |
|----------|-------------|
| `FenceAgentsRemediation` | `far` |
| `FenceAgentsRemediationTemplate` | `fartemplate` |

*(From kubebuilder `+kubebuilder:resource:shortName` on the types.)*

## Related pieces

- **`architecture.md`** — how these components fit together at runtime.  
- **`runbook.md`** — operational commands.

## Scope

This map does **not** document every **bundle** manifest or **RBAC** rule file under **`config/`**.
