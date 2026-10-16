# Raw workload responses and recovery preservation

The final native binary passed **two targeted cases and all 13 automatic baseline scenario IDs** on **OpenShift 4.21.36 / Kubernetes v1.34.9**. Full qualification remains partial. The [structured record](../compatibility/evidence/2026-10-16-raw-responses-4.21.36.json) identifies the exact artifact, source, drivers, observations and limitations.

## Actual cases

| Case | Verified result |
| --- | --- |
| Unfamiliar Deployment field | Create a real `oc new-app --as-deployment-config` source, stage a paused zero-replica target, and patch only the target with `spec.template.spec.securityContext.seLinuxChangePolicy: Recursive`. Confirm the server retains the field. The previous binary's read-only preflight incorrectly accepts it; the current binary refuses both preflight and cutover. Source Pod UID/readiness/HTTP, full source spec, HPA UID/spec and target UID/spec remain unchanged. Explicit rollback and rerun preserve the unfamiliar target field and the original healthy source Pod/HPA. |
| Existing regular CSI volume | Reuse the approved 1Gi gp3 PVC/PV. Regenerate only its verified owned paused-zero target, then plan, stage, preflight, cut over, read a unique file marker from the destination, and recover. Original source spec/HTTP, original application marker, new file marker and PVC/PV UIDs remain intact. No new storage or SCC grant. |

The baseline suite additionally completes schema/dry run, SCC defaults, exclusive/overlap cutover, Service routing, HPA retargeting, image updates, interruption, recovery, safe reruns, stale-plan refusal and constraint preflight on this exact binary. Its nonzero partial result reflects missing supplemental records, not failed automatic scenarios. Earlier extensive new-app, namespace, runtime and recovery sessions remain evidence for their own artifact hashes.

The field test exercises a live Deployment read and recovery update. It does not inject an admission webhook, run a Pod with that field, or prove all newer-field semantics. Deployment create/update replies, HPA replies and representative ReplicaSet admission have separate loopback regression coverage, including fields introduced only on persistence, retained destination identity and safe recovery.

## Failure found and corrected

The first raw-response artifact passed the targeted field case but failed ordinary baseline activation after source retirement. Its typed-to-raw conversion added `spec.template.metadata.creationTimestamp: null`; the actual API omitted that field. Strict comparison therefore reported an admission difference. A server dry run isolated that exact difference, and a local regression reproduced the failure.

Explicit recovery with that original binary restored source configuration and HTTP health while leaving the target paused at zero. Only that run's ownership-labelled resources were subsequently removed after checking source/target UIDs and recovery health. The baseline namespace was verified empty before the corrected run. The failed attempt, diagnostics, recovery and cleanup evidence are retained; it is not counted as a pass.

The correction preserves the original raw server representation for activation and recovery, changing only the reviewed replica count, pause state and image-trigger annotation. Raw baseline validation and ownership/concurrency checks remain mandatory. The rebuilt artifact then passed the full automatic baseline suite and both targeted cases.

## Artifact and validation

- Native binary SHA-256: `41a63e14db9753aecd043d985095b144e9fec0746b9fd040e6613eb866d015b8` (`darwin/arm64`).
- Production Go revision: `ba43309`; raw-response guard: `f41c0aa`; public raw-field driver: `1cb32b5`.
- Kubernetes modules remain aligned at `v0.31.3`.
- Local validation: **230 race tests/subtests, 76.7% coverage**, vet, 20 Python tests, formatting, zero govulncheck findings and zero gosec issues/compiler errors. Five platform builds passed compilation; other platform binaries were not executed live.
- Raw evidence is retained under `.scratchpad/raw-boundaries-20261016`, including both immutable native artifacts, command inputs/results, snapshots, plans, reports, failed attempts and local checks. Credential-free archive metadata is recorded in the structured evidence after packaging.

Final raw-field fixtures and the recovered existing CSI fixture remain for inspection. No new SCC, ClusterRole, PV/PVC, StorageClass or node was provisioned. Older-version review assumptions, broader final-artifact scenarios, restricted-v3 runtime and cross-node storage behavior remain unverified.
