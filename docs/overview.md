# Fence Agents Remediation (FAR) — Overview

## Purpose

**Fence Agents Remediation (FAR)** helps when a node is **unhealthy** and you need to **fully isolate the node by fencing it** (for example **rebooting** or **powering it off** **out of band**) so it cannot harm **shared storage** or the rest of the cluster while you recover. After fencing succeeds, FAR also helps **move workloads** off that node.

FAR is a **remediator**: something else (usually **Node Health Check**, NHC) decides the node is unhealthy and creates a **`FenceAgentsRemediation`** object; FAR **runs** the remediation and **reports status** on that object.

## How it works

1. **Someone creates a FAR object** for a node (often NHC, using a **`FenceAgentsRemediationTemplate`**).
2. FAR **taints the node** with a **NoSchedule** taint (`remediation.medik8s.io/fence-agents-remediation`) so new pods are not scheduled there during remediation.
3. FAR **runs the fence agent** (`fence_*` …) with **`reboot`** or **`off`**. If **action** is not set in parameters, the operator **defaults to `reboot`**. **`reboot`** brings the node back; **`off`** leaves it off until **manual** recovery.
4. After the fence step **succeeds**, FAR applies **`spec.remediationStrategy`**: **`ResourceDeletion`** (delete workloads on the node) or **`OutOfServiceTaint`** (Kubernetes **out-of-service** taint for non-graceful shutdown semantics). The latter is only allowed on clusters where that is **supported** (the operator enforces this).
5. **NHC** can **stop** remediation early via the **`remediation.medik8s.io/nhc-timed-out`** annotation.

## Important details

- FAR and templates are **namespaced** (`fence-agents-remediation.medik8s.io/v1alpha1`). **Secrets** should be in the **same** namespace as the CR.
- Target node: annotation **`remediation.medik8s.io/node-name`** if set; otherwise the CR **name** must match a **Node** in the cluster.
- **Templates** can optionally run **status** checks on a **sample** of nodes to validate fence parameters (see **architecture**).
- Deployments often use **two replicas** and **leader election** for controller availability (see **architecture**).

## Related pieces

| Piece | Role |
|--------|------|
| **NHC** | Creates/deletes FAR CRs; may set **NHC timeout** annotation. |
| **Fence agents** (ClusterLabs-style) | Out-of-band power actions; FAR runs **`fence_*`** binaries from the operator image. |
| **Secrets** | Credentials and parameters for the fence agent CLI. |

## What this file is not

This overview does **not** describe reconcile ordering, webhooks, executor retries, or template status-validation mechanics. Those belong in **architecture**, **failure_modes**, and **runbook**.
