# Fence Agents Remediation (FAR) — Operational runbook

## Purpose

Practical checks for **Fence Agents Remediation**: what to look at when FAR is installed, when a remediation runs, or when something looks stuck. Use **`overview.md`**, **`../ARCHITECTURE.md`**, and **`failure_modes.md`** for behaviour; this page focuses on **commands** and **fields to inspect**.

## Components

| Piece | Role |
|-------|------|
| **Operator deployment** | Reconciles **`FenceAgentsRemediation`** and **`FenceAgentsRemediationTemplate`**, serves **webhooks**, runs **`fence_*`** subprocesses from the pod. |
| **CRDs** | **`fence-agents-remediation.medik8s.io/v1alpha1`** — **`far`**, **`fartemplate`**. |

FAR CRs and referenced **Secrets** are **namespaced** (same namespace for the Secret as for the FAR or Template).

## Operator process (observability & HA)

| Topic | Note |
|-------|------|
| **Metrics** | Default bind **`:8443`**, **secured** (TLS + Kubernetes bearer-token authn/authz via controller-runtime filters — same pattern as sibling medik8s operators); scrape via your **Service** / **ServiceMonitor** if enabled in the bundle. |
| **Health / readiness** | Default **`:8081`** (`cmd/main.go`). |
| **Webhooks** | TLS often uses certs under **`/apiserver.local.config/certificates`** when OLM injects them; **HTTP/2** for metrics/webhooks is **disabled by default** for hardening (`--enable-http2` to re-enable). On **OpenShift**, TLS settings for both servers follow the cluster's **`APIServer`** TLS profile, and the operator restarts if that profile changes—see **`../ARCHITECTURE.md`**. |
| **Leader election & replicas** | **`--leader-elect`** is a **flag**; the **shipped CSV** usually runs **two** operator replicas with leader election so **one** active reconciler holds the lease. If behaviour looks duplicated or “stuck between pods”, check the **Deployment** args. |

## Admission defaults (what operators notice)

| Topic | Note |
|-------|------|
| **Legacy shared secret** | **Mutating** admission on **FAR** and **Template** may **set** **`sharedSecretName`** to **`fence-agents-credentials-shared`** on **create** when that Secret exists, or **clear** a stale default when it does not. **Validating** admission on **Template update** blocks **removing** that name while the Secret still exists—see **`failure_modes.md`**. |
| **Multiple-templates annotation** | **Template** mutator sets **`remediation.medik8s.io/multiple-templates-supported`** (medik8s common) when unset—normally informational, not a failure. |

## Prerequisites

| Requirement | Note |
|-------------|------|
| **Webhooks + CRDs** | Match the Kubernetes / OLM bundle you installed. |
| **OutOfServiceTaint** | Only on clusters where that strategy is **supported** (operator probes **server version** at startup; roughly **Kubernetes 1.26+**). |
| **Fence connectivity** | Operator pod must reach **BMC / cloud / hypervisor** endpoints your **`fence_*`** agent uses. |
| **Secrets** | Present in the **same namespace** as the FAR or Template; referenced by **`sharedSecretName`** / **`nodeSecretNames`**. |

## Quick health checks

Replace **`<ns>`** with the namespace where FAR objects (and usually the operator) live.

```bash
kubectl get far -A
kubectl get fartemplate -A
kubectl describe far -n <ns> <name>
kubectl describe node <node-name>
kubectl get pods -n <ns> -l app.kubernetes.io/name=fence-agents-remediation-operator -o wide
```

On OpenShift, **`oc`** works the same way.

## What to inspect on `FenceAgentsRemediation`

| Area | What to look for |
|------|------------------|
| **Conditions** | **Processing**, **FenceAgentActionSucceeded**, **Succeeded** — fencing **ran**, **succeeded**, and **post-fence** step **finished**. |
| **Condition messages / reasons** | Human-readable state and errors. |
| **Annotations** | **`remediation.medik8s.io/node-name`** if the node name differs from the CR **name**. |
| **Annotations** | **`remediation.medik8s.io/nhc-timed-out`** — NHC asked FAR to **stop** driving remediation. |
| **Finalizers** | **`fence-agents-remediation.medik8s.io/far-finalizer`** — delete blocked until FAR **removes taints** and drops finalizer. |

## What to inspect on the Node

| Taint / note | When it appears |
|--------------|-----------------|
| **`remediation.medik8s.io/fence-agents-remediation`** (NoSchedule) | During / around FAR remediation. |
| **`node.kubernetes.io/out-of-service`** (NoExecute, `nodeshutdown`) | When **`remediationStrategy`** is **OutOfServiceTaint**. |

**`kubectl describe node`** shows taints and **events**.

## What to inspect on `FenceAgentsRemediationTemplate`

| Area | What to look for |
|------|------------------|
| **`spec.statusValidationSample`** | **Unset or zero** → status validation **skipped**. If the requested sample is **larger** than the number of nodes in the spec, the operator **caps** it (warning **event**). |
| **`status.conditions`** | **FenceAgentStatusValidationSucceeded** and messages. |
| **`status.validationFailed`** | Per-node messages from **`fence_* --action=status`**. |

## FAR spec knobs (operations)

| Area | Effect |
|------|--------|
| **`agent`** | **`fence_*`** binary name; must exist **in the operator image** (`/usr/sbin/`). |
| **`retrycount`**, **`retryinterval`**, **`timeout`** | Subprocess **retries** and **per-attempt** timeout. |
| **`sharedparameters`**, **`nodeparameters`** | CLI args; **`{{.NodeName}}`** templating supported. |
| **`sharedSecretName`**, **`nodeSecretNames`** | Credentials / params from **Secrets** (same namespace). |
| **`remediationStrategy`** | **ResourceDeletion** vs **OutOfServiceTaint** (latter only if cluster **supports** it). **ResourceDeletion** uses the shared **`DeletePods`** helper; operator **RBAC** includes **pods** and related cleanup (e.g. **volumeattachments**) as shipped. |

If **`action`** is not set in parameters, the operator **injects** **`reboot`** when building the CLI — confirm **`reboot`** vs **`off`** matches intent.

## Events

FAR emits **events** on the FAR object and on the **Node**. Medik8s remediation-related messages often include **`[remediation]`** in the text (use **`kubectl get events`** with your preferred filter).

## Debugging checklist

1. **Node exists** under the name FAR uses (**annotation** vs **metadata.name**)?  
2. **NHC timeout** annotation set — should remediation be **stopped**?  
3. **Operator logs** — fence **stderr**, retries, timeouts.  
4. **Network** from operator pod to **management plane** (BMC / cloud / etc.).  
5. **Node taints** and **finalizer** — delete stuck on cleanup?  
6. **Templates** — read **`status.validationFailed`**; adjust **sample** or parameters.  
7. **Fence already running** — only one subprocess per FAR **UID**; if nothing progresses, check for a **hung** fence or timeouts (**`failure_modes.md`** §9).

## Related pieces

- **`failure_modes.md`** — symptom → cause mapping.  
- **`../ARCHITECTURE.md`** — reconcile order and internals.

## Scope

This runbook does **not** replace **fence agent** vendor docs (IPMI, cloud APIs) or **NHC** configuration guides.
