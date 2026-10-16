# Cluster admin-policy refusal before source retirement

The final native binary passed **one aggregate live case containing six policy variants**, successful migration/recovery after policy removal, and **all 13 automatic baseline scenario IDs** on **OpenShift 4.21.36 / Kubernetes v1.34.9**. Full release qualification remains partial. See the [structured evidence](../compatibility/evidence/2026-10-16-admin-policy-4.21.36.json).

## Actual observations

A fresh real `oc new-app --as-deployment-config` application runs with a fixed one-replica HPA and an HTTP response marker. Each temporary cluster policy is restricted to that new namespace through its unique run label. No existing policy is overwritten; the driver refuses to replace an existing BANP.

| Policy variants | Selector | Required observation |
| --- | --- | --- |
| ANP and BANP subjects | Destination migration label | Previous binary's read-only preflight accepts it; current preflight and cutover refuse before writes |
| ANP and BANP ingress peers | Source's DC-added `deployment` label | Actual source labels distinguish the old Pods from the proposed destination; both operations refuse |
| ANP and BANP subjects | Future `pod-template-hash` label | Current operations refuse manual review rather than treating a controller-generated label as absent |

All **12 refusals** preserve the source Pod UID/readiness/HTTP, complete DC spec and UID, HPA UID/spec, target UID/spec and `staged` journal phase. Each policy is removed after checking its UID and ownership label. The final policy identity set matches the initial set. With the policies removed, preflight, cutover, HTTP health, explicit rollback and rollback rerun pass; the source spec and HPA spec recover and the target remains paused at zero.

The baseline suite independently completes schema/dry run, SCC defaults, exclusive/overlap cutover, Service routing, HPA retargeting, image updates, interruption, recovery, reruns, stale-plan refusal and constraint preflight. Its partial/nonzero result is due to absent supplemental records, not a failed automatic scenario.

## Findings and limits

The first guard (`93efcf3`) passed four live subject/peer variants and 13 baseline IDs. Follow-up contract tests then reproduced another gap: the [Deployment controller adds `pod-template-hash`](https://kubernetes.io/docs/concepts/workloads/controllers/deployment/#pod-template-hash-label) after Pod dry run. Comparing the staged template alone treated that future label as absent. `132542b` requires manual review for namespace/admin Pod selectors that depend on it. The rebuilt binary passed all six live variants. Earlier artifact results remain separately recorded.

The converter performs read-only discovery and inventory. Denied reads, unknown policy API versions/resources/fields and changing subject/peer Pod selection block retirement. Namespace labels are fetched only when policies exist; ordinary generation/planning and explicit recovery need no additional policy inventory rights. See [RBAC](rbac.md).

These tests verify selector refusal and preservation, not every rule-priority, port, CNI, node/IP/domain or network-flow outcome. Namespace NetworkPolicy hash refusal and egress-peer variants have local contract coverage; the six final live variants use ANP/BANP subjects and ingress peers. No previous binary performs cutover. No SCC, ClusterRole, node, StorageClass or PV/PVC is provisioned. Initial/final fixture namespaces remain with recovered sources and paused targets; all temporary cluster policies are removed.

## Exact artifact and evidence

- Native SHA-256: `bf3664a031885ca341abe3102c5b45bd12a2f077dc020a5ca54dc812ce901eb9`, `darwin/arm64`, production revision `132542b`, final driver `0159651`.
- Local checks: **286 race tests/subtests, 77.2% coverage**, vet, 20 Python tests, formatting, zero vulnerability findings and zero source-scanner issues/compiler errors. Five platform builds passed compilation; other platform artifacts were not executed live.
- Raw commands, policy objects, source/target snapshots, plans, reports, local regressions and both initial/final attempts are retained under `.scratchpad/admin-policy-20261016`. Credential-free archive and source-rebuild metadata are appended to the structured record after packaging.

Older-version review assumptions and broader final-artifact qualification remain unfinished. The [4.14 candidate screen](../compatibility/reviews/4.14-candidates.json) records review progress without asserting old-server integration passes.
