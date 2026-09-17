# Fence Agents Remediation (FAR) — Failure modes

## Purpose

This is a **symptom-first** guide: what you might see when something goes wrong with FAR, and what usually causes it. Use it with **`architecture.md`** (how it works) and **`runbook.md`** (commands).

Behaviour references **`internal/controller/fenceagentsremediation_controller.go`**, **`internal/controller/fenceagentsremediationtemplate_controller.go`**, **`api/v1alpha1/fenceagentsremediation_params.go`**, **`pkg/cli/cliexecuter.go`**, **`pkg/utils/conditions.go`**, unless noted.

## Governing constants (selected)

| Name | Typical meaning |
|------|------------------|
| **FAR finalizer** | `fence-agents-remediation.medik8s.io/far-finalizer` |
| **FAR remediation taint** | `remediation.medik8s.io/fence-agents-remediation` (NoSchedule) |
| **NHC timeout annotation** | `remediation.medik8s.io/nhc-timed-out` |
| **Fence spec defaults** | **`retrycount`** 5, **`retryinterval`** 5s, **`timeout`** 60s (overridable on the CR) |
| **Template status command timeout** | **15s** for `fence_* --action=status` in the template reconciler |

---

## 1. Target node does not exist

**Symptom:** Conditions reflect **node not found**; events suggest the CR does not match a cluster node.

**Behaviour:** FAR resolves the target from **`remediation.medik8s.io/node-name`** if set, otherwise **`metadata.name`**. If that **Node** is missing, FAR does **not** run fencing and records a terminal **not-found** outcome.

**Ops:** Align CR **name** and **node-name** annotation with **`kubectl get node`**; confirm NHC or automation created the FAR for the correct node.

---

## 2. Admission / webhook rejection (create or update)

**Symptom:** API server rejects the **FAR** or **FenceAgentsRemediationTemplate** with a webhook error.

**Behaviour (common):**

- **OutOfServiceTaint** on Kubernetes **below 1.26** — unsupported at admission (version probe at operator startup).
- **Agent** name does not match a **`fence_*`** binary present under **`/usr/sbin/`** in the operator image.
- **Invalid parameters:** missing mandatory parameter material, **`{{.NodeName}}`** template syntax errors, or **`action`** not **`reboot`** / **`off`**.
- **Legacy shared secret:** **mutating** admission on **FAR** and **Template** may **set** or **clear** **`sharedSecretName`** relative to Secret **`fence-agents-credentials-shared`**; **validating** admission blocks **removing** that default **name** from a **template** while the Secret still exists in the namespace.
- **Template mutator:** sets **`remediation.medik8s.io/multiple-templates-supported`** (medik8s common) when unset—normally **not** a failure path.

**Ops:** Inspect webhook / operator logs; fix spec or cluster version; for legacy secret, delete the Secret before clearing the field, or keep the field until migration is done.

---

## 3. Secrets and parameters misconfiguration

**Symptom:** Errors building the fence CLI; Secret not found; duplicate parameter errors.

**Behaviour:** Secrets are read from the **same namespace** as the FAR (or template’s embedded spec). Shared and per-node Secrets merge with inline **SharedParameters** / **NodeParameters**; **node** values override **shared** for the same key. **Duplicate** keys between secrets and other sources fail validation.

**Ops:** Verify Secret **name**, **namespace**, keys, and **NodeSecretNames** entries for the target node.

---

## 4. Fence command failure or timeout

**Symptom:** **FenceAgentActionSucceeded** does not become **True**; conditions show **failed** or **timed out**; operator logs show subprocess **stderr** / non-zero exit.

**Behaviour:** **`Executer`** retries with **`retrycount`**, **`retryinterval`**, **`timeout`** per attempt. Exhausted retries or per-attempt timeout maps to **FenceAgentFailed** or **FenceAgentTimedOut**; remediation does **not** proceed to successful post-fence cleanup.

**Ops:** Validate BMC / cloud / hypervisor parameters, network reachability, agent-specific timeouts, and intended **`reboot`** vs **`off`**.

---

## 5. NHC interrupted remediation

**Symptom:** **`nhc-timed-out`** annotation set; remediation **stopped** / interrupted conditions.

**Behaviour:** FAR **cancels** the executor routine for that UID. If the CR is **deleting**, **`handleFARDeletion`** still runs (taints + finalizer). If not deleting, FAR marks **interrupted by NHC** and stops progressing.

**Ops:** Review NHC timeout / escalation; treat annotation changes as part of a deliberate recovery plan.

---

## 6. Stuck delete: taints or finalizer

**Symptom:** FAR or **Node** not converging; logs show **requeue** after node **update conflict**.

**Behaviour:** Deletion removes **out-of-service** taint (if strategy was **OutOfServiceTaint**), then FAR **NoSchedule** taint, then **finalizer**. **Conflict** on Node patch triggers **short requeue** (e.g. **1s**).

**Ops:** **`kubectl describe`** FAR and Node; check concurrent taint editors; inspect events.

---

## 7. Post-fence cleanup failure

**Symptom:** Fence **succeeded** but **Succeeded** never becomes **True**; errors around **DeletePods** or **out-of-service** taint.

**Behaviour:** **ResourceDeletion** path calls **`commonResources.DeletePods`**. **OutOfServiceTaint** path appends **`node.kubernetes.io/out-of-service=nodeshutdown:NoExecute`**. API or RBAC errors surface in logs.

**Ops:** Operator logs and RBAC; confirm strategy matches cluster capabilities for **OutOfServiceTaint**.

---

## 8. Template status validation failures

**Symptom:** **`FenceAgentStatusValidationSucceeded`** **False**; **`status.validationFailed`** entries; or validation appears **skipped**.

**Behaviour:** **`statusValidationSample`** **nil** or **zero** → validation **skipped**. Otherwise a **sample** of nodes (count or percent) runs **`fence_* --action=status`** with a **15s** timeout; stdout must match **ON**-style patterns expected by the controller (varies by agent). Invalid **IntOrString** fails validation; sample larger than node list is **capped** (warning event).

**Ops:** Read **`validationFailed`** messages; adjust **sample**, fence params, or management connectivity; allow for agents with slow **status** responses.

---

## 9. Fence “already running” (no second subprocess)

**Symptom:** Logs indicate a fence agent is **already running** for this FAR **UID**; reconcile returns without starting another.

**Behaviour:** **`AsyncExecute`** keeps **one** background run per UID; duplicate starts are ignored.

**Ops:** If stuck, check whether the subprocess is **hung**, **timeouts** are too large, or the management plane is not completing the operation.

---

## Related pieces

- **`architecture.md`** — reconcile order and components.
- **`runbook.md`** — `kubectl` flows.

## Scope

This document does **not** list every **Event** reason string. It does **not** replace **`runbook.md`** for copy-paste commands.
