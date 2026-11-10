# Identity-bound Redfish Off remediation

Safety-mode is an explicit alternative to the default fence-agent subprocess path. It requires a selected Node identity, authenticated Redfish, an explicit ComputerSystem URI, and `OutOfServiceTaint`. It does not support IPMI, reboot, direct pod deletion, or manually created remediation CRs. Without safety-mode, existing CLI behavior remains unchanged; adding `safetyPolicy` without configuring safety-mode is refused rather than falling back to the CLI.

## Operator configuration

Start the operator with these flags in addition to its normal leader-election, webhook, health, and metrics configuration:

```text
--safety-mode=true
--trusted-nhc-creator=system:serviceaccount:operators:node-healthcheck
--bmc-credential-namespace=fence-agents-credentials
--redfish-ca-file=/etc/redfish/ca.crt
```

The creator must be one exact service-account username. Missing creator, namespace, or trusted PEM CA configuration prevents startup. Configure the webhook with failure policy `Fail`; an owner reference or annotation is not authorization. The dedicated credential namespace also contains the FAR objects and templates. CR admission permits only the configured NHC service account to create safety-mode remediations. Metadata-only updates still permit NHC timeout/deletion and FAR finalizer cleanup; the effective remediation spec cannot change.

The Secret, FenceAgentsRemediation, and FenceAgentsRemediationTemplate caches watch only the configured BMC namespace in safety-mode. Identity and credential checks use the uncached API reader. This is namespace-scoped caching, not removal of those caches. Default-mode cache behavior is separate.

## Template and credential contract

A template selects `spec.template.spec.safetyPolicy: RedfishOffV1`, `agent: fence_redfish`, and `remediationStrategy: OutOfServiceTaint`. It must omit `nodeBinding` and leave generic `statusValidationSample` unset or zero. Safety-mode never runs a generic status subprocess.

Only `--ip`, optional `--ipport`, `--systems-uri`, and explicit `--action: off` are accepted as parameters. Shared parameters and per-node overrides use the existing maps, but parameter aliases, Go templates, inline credentials, and extra CLI options are rejected. The host must not contain a scheme, path, user information, or an embedded port; use `--ipport` for a nondefault port. The ComputerSystem URI must identify one member under `/redfish/v1/Systems/`, not a collection or discovered default.

For each selected node, reference exactly one Secret through either `sharedSecretName` or its `nodeSecretNames` entry. The Secret must contain only nonempty literal keys `username` and `password`. CLI-style keys and target configuration are not accepted in Secrets.

```yaml
apiVersion: fence-agents-remediation.medik8s.io/v1alpha1
kind: FenceAgentsRemediationTemplate
metadata:
  name: redfish-off
  namespace: fence-agents-credentials
spec:
  template:
    spec:
      safetyPolicy: RedfishOffV1
      agent: fence_redfish
      remediationStrategy: OutOfServiceTaint
      retrycount: 3
      retryinterval: 5s
      timeout: 30s
      sharedSecretName: redfish-credentials
      sharedparameters:
        --action: off
        --systems-uri: /redfish/v1/Systems/1
      nodeparameters:
        --ip:
          worker-a: 192.0.2.10
```

`retrycount` is bounded to 1–5, `retryinterval` to 1–30 seconds, and each guarded verification to 1–60 seconds. Action attempts and their most recent reservation time are persisted before the guarded call, so status events and leader restarts do not reset the budget or retry spacing. The reservation timestamp is rounded forward to the next whole second because its JSON representation has second precision; this can add up to one second to the wait without shortening the configured interval. After the budget is exhausted, a read-only same-system Off observation may still complete verification; no further ForceOff is permitted.

## Node-binding wire contract

For `RedfishOffV1`, NHC injects this typed field into the generated FAR CR, using the same Node object selected by its health decision:

```yaml
nodeBinding:
  version: v1
  name: worker-a
  uid: <SELECTED_NODE_UID>
  systemUUID: 00112233-4455-4677-8899-aabbccddeeff
```

`name` and `uid` identify the selected Kubernetes Node. `systemUUID` is its canonical lowercase `status.nodeInfo.systemUUID`; absent, malformed, all-zero, or all-ones UUIDs are rejected. FAR validates the binding against an uncached Node read. The existing `remediation.medik8s.io/node-name` annotation is only a routing hint and, when present, must equal `nodeBinding.name`. Neither CR name nor annotation can supply a missing binding. A recreated same-name Node requires a new remediation decision and a new CR.

The binding is immutable. CRD transition rules and admission also prevent changes to the effective safety-mode CR spec. Runtime compares that spec and generation with the persisted snapshot before effects; it does not silently refresh an old binding. Legacy CRs without a binding are refused by a safety-mode operator.

Each fresh admission/action/release identity gate makes an uncached, unfiltered List of known Nodes. Exactly one case-normalized matching system UUID must identify the bound Node name and UID. Duplicates, a missing or replaced target, a terminating target, or a failed, forbidden, or incomplete List refuse power actions and OOS release. This costs O(N) over known Nodes at each gate and requires cluster-scoped Node list permission; it does not use the Secret cache. Cleanup remains scoped to the bound UID and resourceVersion rather than adopting a duplicate-UUID Node.

## Verification and release

Before power effects, FAR persists `status.safetySnapshot`, bound to the FAR UID/generation, selected Node binding, effective spec digest, normalized HTTPS endpoint, explicit system URI, public CA digest, and Secret namespace/name/UID/resourceVersion. This snapshot is not Off evidence. It contains no credentials or credential-derived hashes. A changed Secret version, target, trust configuration, or effective spec invalidates the snapshot; the old CR cannot rebind to the new configuration.

The guarded Go Redfish path verifies the server certificate against the configured CA, disables redirects and proxies, and bounds request time and response size. It reads the selected ComputerSystem UUID before accepting its power state. Already-Off requires a matching identity and needs no reset. Otherwise the reset action must address that same system and origin; FAR rechecks the Kubernetes/configuration preconditions and UUID before posting `ForceOff`, then polls the same system until its UUID still matches and `PowerState` is exactly `Off`. HTTP acceptance alone, malformed responses, and unrecognized power states do not establish success.

Only a completed observation produces `status.offProof`, including its snapshot and confirmation time. A restart with an unfinished snapshot must perform verification again. Persisted success conditions alone do not permit release. Before adding OOS, FAR obtains a fresh same-system UUID/Off observation, rechecks the current FAR and Secret version, reads the Node uncached, and updates that matching Node using its current resourceVersion. A conflict requires another full release check, not a name-based refetch-and-update. Completed release does not repeatedly perform BMC operations on its own status events.

Deletion cleanup removes OOS and remediation taints only from the bound Node UID, using the fetched object's resourceVersion. A replacement Node is left untouched. A changed persisted binding or effective spec refuses cleanup rather than adopting a new target.

## RBAC

`config/rbac/safety/cluster_role.yaml` grants cluster-scoped Node identity/taint access and platform TLS-profile reads, without Node deletion, pod deletion, or VolumeAttachment deletion. `config/rbac/safety/credential_role.yaml` is the namespaced safety Role: it grants Secret read/list/watch and FAR/template read/update/status/finalizer access only in the configured BMC namespace. Set its namespace to the configured value and bind both roles to the operator's service account instead of retaining broader default runtime grants. Leader-election and metrics-authentication permissions remain separate and must be retained where required by the deployment. The generated default runtime role also omits the unused Node deletion permission.

The cache configuration and namespace checks are not a replacement for RBAC. Do not grant cluster-scoped Secret access or retain another binding that widens the safety-mode account's permissions.

The default OLM bundle retains legacy-mode permissions and does not install the dedicated safety-mode roles or operator flags. Synchronized bundle CRDs and CSV permissions do not make it a least-privilege safety-mode deployment. Safety-mode activation on that default deployment is unsupported without separately verified configuration and grant replacement through its deployment owner.

## Operational limits

An authenticated BMC UUID response and a Node-reported system UUID are not hardware attestation. The known-Node uniqueness check is a point-in-time observation, not proof that an unregistered physical machine does not share the UUID. Validate canonical equivalence on supported firmware before enrollment. Kubernetes identity/configuration reads and the Redfish POST are separate operations, not a transaction; external actors can change state after the final check. Coordinate Node replacement with remediation. Off is a point-in-time observation: the BMC read and Kubernetes taint update cannot be atomic with an administrator or another controller powering the machine on. Prevent automatic or concurrent power restoration throughout remediation and recovery. FAR does not prove that independent node/resource deletion controllers are quiescent.

OOS is a release trigger for Kubernetes controllers, not proof that pods were deleted, volumes detached, or storage replicas recovered. Local HTTP and API tests validate protocol, identity, versioning, RBAC, and concurrency behavior; they do not establish physical power isolation, firmware correctness, detach completion, or storage safety.
