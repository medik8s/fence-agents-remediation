# Fence Agents Remediation (FAR) — Architecture

## Purpose

This document explains **how the FAR operator runs**: startup, **`FenceAgentsRemediation`** reconciliation, **`FenceAgentsRemediationTemplate`** validation, **webhooks**, and the **fence CLI executor**. Read **`overview.md`** first for the product-level picture.

## API scope

- **Group:** `fence-agents-remediation.medik8s.io`, **`v1alpha1`**
- **Kinds:** **`FenceAgentsRemediation`** (short **`far`**), **`FenceAgentsRemediationTemplate`** (short **`fartemplate`**)
- **Scope:** **Namespaced**. **Secrets** referenced by a CR are read from the **same namespace** as that CR.

## Operator startup (`cmd/main.go`)

The process starts a standard **controller-runtime** manager: a **secured metrics** server (default **`:8443`**, TLS + Kubernetes bearer-token authn/authz via controller-runtime's `metrics/filters`), **health/readiness** probes, and **admission webhooks** (TLS; OLM installs often inject certificates under **`/apiserver.local.config/certificates`** via `configureWebhookOpts`). **HTTP/2** for metrics/webhooks is disabled by default (forces **HTTP/1.1**, CVE hardening); **`--enable-http2`** re-enables it.

On **OpenShift**, the operator fetches the cluster's **`APIServer`** TLS profile at startup (`openshifttls.FetchAPIServerTLSProfile`) and applies its cipher/version settings to **both** the metrics and webhook servers; off OpenShift (profile lookup returns NotFound/NoMatch) it falls back to default Go TLS settings. A **`SecurityProfileWatcher`** runnable then watches for profile changes at runtime and **restarts the process** (cancels the manager context) if the profile changes.

At startup the operator also asks the API server for its **Kubernetes version** via **`validation.NewOutOfServiceTaintValidator`** (`pkg/validation`). If the version is **below 1.26**, **`IsOutOfServiceTaintSupported()`** is **false**, which is pushed into the **`api/v1alpha1`** package via **`SetOutOfServiceTaintSupported`**; **admission** rejects the **`OutOfServiceTaint`** strategy while that flag is false.

A shared **`Executer`** (`pkg/cli`) is constructed and passed to both the **FAR** reconciler and the **Template** reconciler: it runs **`fence_*`** subprocesses for real remediations (**async**) and for template **status** checks (**sync**).

Both reconcilers are registered, and **mutating/validating webhooks** for FAR and Template are wired via **`internal/webhook/v1alpha1`** (**`SetupFARWebhookWithManager`**, **`SetupFARTemplateWebhookWithManager`**) — the **validators** (**`FARValidator`**, **`FARTemplateValidator`**) themselves live in **`api/v1alpha1/fenceagentsremediation_params.go`**, while the webhook package supplies the **defaulters** (legacy shared-secret name handling) and manager wiring.

**Leader election** is controlled by a **`--leader-elect`** flag. The **shipped** OLM deployment usually runs **two replicas** with leader election enabled so only one reconciler is active at a time—the **default in `cmd/main.go`** may differ from what your **ClusterServiceVersion** actually sets. Leader election ID in code: **`cb305759.medik8s.io`**.

## FAR reconciliation (`internal/controller/fenceagentsremediation_controller.go`)

Each loop tries to move one **`FenceAgentsRemediation`** forward safely.

1. **Load** the FAR. If it no longer exists, stop.
2. **Defer status update** at the end of the function. If the object is deleting and the **finalizer** is already gone, status update may be skipped.
3. **Target node:** **`GetNodeName`** uses annotation **`remediation.medik8s.io/node-name`** (medik8s common) if set; otherwise **`metadata.name`**. **`GetNodeWithName`** loads the **Node**. If the node does **not** exist, FAR sets **node-not-found** conditions and an event—there is no healthy fencing target.
4. **NHC timeout:** if **`remediation.medik8s.io/nhc-timed-out`** is present, FAR stops driving work: it removes any **executor** routine for this object’s UID. If the CR is **deleting**, it runs **deletion cleanup** (taints + finalizer); otherwise it marks remediation **interrupted by NHC** and stops.
5. **Finalizer:** if the FAR is not deleting and does not yet have **`fence-agents-remediation.medik8s.io/far-finalizer`**, FAR adds it, records **remediation started**, emits events, and **requeues immediately** so the next pass continues with stable metadata.
6. **Deleting:** if the finalizer is present and **DeletionTimestamp** is set, and **Succeeded** was not reached, FAR cancels executor work, then runs **deletion cleanup**.
7. **Remediation taint:** FAR ensures the node has **`remediation.medik8s.io/fence-agents-remediation:NoSchedule`** (`pkg/utils/taints.go`).
8. **Fence phase:** when **Processing** is true but **FenceAgentActionSucceeded** is not yet true: if a fence command is **already running** for this UID, return; otherwise **`BuildFenceAgentParams`**, assemble argv, call **`AsyncExecute`** with **`retrycount`**, **`retryinterval`**, and **`timeout`** from the spec, and record that the fence agent was executed.
9. **Post-fence:** when **FenceAgentActionSucceeded** is true and **Succeeded** is not yet true, FAR applies **`spec.remediationStrategy`**:
   - **`ResourceDeletion`** or **empty string** (legacy objects): delete pods on the node via **`commonResources.DeletePods`**, then mark **remediation finished successfully**.
   - **`OutOfServiceTaint`**: append **`node.kubernetes.io/out-of-service=nodeshutdown:NoExecute`** (with **TimeAdded**), then the same success path.
   - Any other value should be blocked by the API; the controller treats unknown values as an error and does not advance.

On full success, FAR **removes** the executor entry for this UID and emits completion events.

### Deletion cleanup (`handleFARDeletion`)

If **OutOfServiceTaint** was used, FAR **removes** that taint first. It then **removes** the FAR **NoSchedule** taint. **Node update conflicts** trigger a **short requeue** (e.g. one second) instead of failing permanently. Finally FAR **removes the finalizer** and updates the CR.

### Status and cache

After **`Status().Update`**, the reconciler **polls** (up to a few seconds) until **`lastUpdateTime`** reflects the write, so the next reconcile does not act on **stale** conditions.

## Fence executor (`pkg/cli/cliexecuter.go`)

**`AsyncExecute`** starts **one background run per FAR UID**; a second call for the same UID is ignored.

The command runs under **exponential backoff**: **`retryCount`** attempts, **`retryInterval`** spacing, each attempt bounded by **`timeout`** via **`exec.CommandContext`**.

When the command finishes, the executor finds the FAR by **UID** (listing FARs), maps the outcome to **succeeded**, **failed**, or **timed out**, updates **conditions** via **`pkg/utils/conditions.go`**, and **updates status** with retries on conflict.

**`SyncExecute`** supports the template reconciler’s **`--action=status`** checks (fixed short timeout there, separate from FAR spec timeouts).

## Conditions (`pkg/utils/conditions.go`)

- **Processing** — true after remediation has **started** (finalizer path); false when finished successfully, failed, timed out, node not found, or interrupted by NHC.
- **FenceAgentActionSucceeded** — true when the fence **CLI** succeeded.
- **Succeeded** — true after **resource deletion** or **out-of-service** taint step completes.

Reason strings include **RemediationStarted**, **FenceAgentSucceeded**, **FenceAgentFailed**, **FenceAgentTimedOut**, **RemediationFinishedSuccessfully**, **RemediationFinishedNodeNotFound**, **RemediationInterruptedByNHC**.

## Parameters, secrets, admission (`api/v1alpha1/fenceagentsremediation_params.go`, `internal/webhook/v1alpha1`, `pkg/validation`, `pkg/template`)

**`BuildFenceAgentParams`** merges **SharedParameters** and **NodeParameters** with data from **SharedSecretName** and **NodeSecretNames** (same namespace as the FAR). **Per-node** values override **shared** keys. Secret keys must not **duplicate** an already-set parameter key.

String values may use **Go template** syntax with **`{{.NodeName}}`** (`pkg/template/template.go`).

**`action` / `--action`** must be **`reboot`** or **`off`**. If still absent after merge, the builder **adds `--action=reboot`**.

**Admission** checks: the agent name must exist as a file under **`/usr/sbin/`**; **OutOfServiceTaint** is rejected when the cluster is too old; shared-parameter templates must parse; **`BuildFenceAgentParams`** is exercised for each node name in the spec (or a **dummy** name when none are listed).

### Legacy shared secret name

The old default **`fence-agents-credentials-shared`** is handled by **mutating** admission (may set or clear the field depending on whether that Secret exists) and **validating** admission on Template **update** (cannot remove the legacy name from the spec while the Secret still exists).

The **Template** mutator also sets **`remediation.medik8s.io/multiple-templates-supported`** when unset (medik8s common annotation).

## Template reconciliation (`internal/controller/fenceagentsremediationtemplate_controller.go`)

Optionally, for each **FenceAgentsRemediationTemplate**, the operator validates fence **reachability / credentials** by running **`fence_<agent> --action status ...`**, reusing parameter building but **avoiding** duplicate action flags.

**`statusValidationSample`** is an **integer or percentage** of nodes named in the template spec. **Unset or zero** means **skip** validation entirely. Invalid values set a **failed** validation condition and emit a warning. If the sample is larger than the node list, it is **capped** (with a warning).

Validation advances **one sampled node per reconcile** and requeues between nodes. The **status** subprocess uses a **15 second** timeout in controller code. **Success** is inferred from **stdout** containing patterns like **STATUS: ON** or a leading **ON** (agent output varies).

The template **status** exposes **`FenceAgentStatusValidationSucceeded`**, plus **`validationFailed`** / **`validationPassed`** maps per node name.

## Related pieces

- **`overview.md`** — what FAR is for.
- **`failure_modes.md`** — symptoms and failure behaviour.
- **`runbook.md`** — operational commands and checks.
- **`code_map.md`** — repository file index (`github.com/medik8s/fence-agents-remediation`).

## Scope

This document does **not** enumerate every **Event** reason or RBAC verb. It does **not** replace **`runbook.md`** for step-by-step operations.
