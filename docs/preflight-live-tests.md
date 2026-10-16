# Constraints checked before source retirement

The migration tool now checks admission, quotas, placement and availability constraints **before changing an HPA or stopping a DeploymentConfig**. A separate `--mode preflight --plan-file PATH` command assesses a staged migration. Cutover repeats the assessment immediately before its first source/HPA mutation, so an earlier successful assessment cannot authorize a later retirement.

The JSON report and plan progress contain a timestamped `constraints` assessment, desired/capacity replicas, individual checks, blockers and warnings. A refusal returns a nonzero exit and leaves the journal `staged`, the destination paused at zero, and the original source and HPA unchanged. Standalone preflight performs reads, access reviews and API dry runs; it does not start a test Pod or persist workload changes.

## Actual cluster results

All **16 constraint scenarios passed** on **OpenShift 4.21.36 / Kubernetes v1.34.9**, using actual `oc new-app --as-deployment-config` sources in isolated namespaces. Constraints were introduced after staging while sources remained healthy. Each refusal was checked through both standalone preflight and cutover; source specs, replicas, availability, application HTTP response and original HPA identity/spec were verified unchanged.

| Scenario | Verified behavior |
| --- | --- |
| Unconstrained application | Preflight succeeds without changing the source/HPA; cutover, health verification and recovery succeed. |
| Namespace Pod quota | Pod admission refuses a destination when the source already consumes the hard limit. |
| Cumulative replica quota | One dry-run Pod fits, but the full destination plus rolling surge does not; aggregate accounting refuses retirement. |
| ReplicaSet object quota | ReplicaSet admission fails before any source/HPA mutation. |
| LimitRange | A new maximum rejects the proposed container while the existing source remains healthy. |
| Namespace node selector | Admission adds an unsatisfiable selector; placement assessment blocks retirement. |
| Node capacity | Admission adds a valid but unschedulable memory request; cluster allocation checks block retirement. |
| NetworkPolicy | Different source/destination selection blocks retirement. |
| Missing service account | Pod admission rejects a removed workload service account; the already-running source remains available. |
| Service-account SCC denial | A source started with a temporary lab-only grant remains healthy after grant removal; the actual workload identity cannot admit a replacement, and preflight refuses. Admin access does not mask the denial. |
| Shared ClusterResourceQuota | Usage in a second selected namespace counts against the shared limit; namespace-local headroom does not incorrectly permit retirement. |
| Changed after preflight | A successful preflight is followed by a tighter quota; cutover reassesses and refuses without stopping the source. |
| Zero replicas | Zero stays zero through preflight, cutover and recovery without requiring Pod capacity. |
| Runtime-label NetworkPolicy | A policy selects the DC controller-added `deployment` label absent from the template; comparison against actual source Pods catches the difference. |
| PodDisruptionBudget | Exclusive retirement would exceed allowed disruptions; preflight blocks even though controller scaling could bypass eviction protection. |
| Availability-preserving overlap | An absolute `minAvailable: 1` budget selects both workloads; the reviewed overlap procedure succeeds and leaves a healthy active destination. |

The shared-quota case records `pods used=3 additional=3 hard=4`. These are cumulative checks, beyond the API acceptance of a single dry-run object. All current source consumption remains counted; capacity must cover the destination's reviewed replicas plus configured rolling surge. An exclusive migration that would only fit after downtime therefore requires temporary headroom or a reviewed manual procedure.

All **26 targeted cases passed**: the 16 constraint cases, three new-app cases (grouped containers, init containers, actual Docker BuildConfig build/rebuild), six multi-namespace cases, and one regular-volume recheck. The baseline runner also passed its **13 automatic scenario IDs**, including interruption, stale-plan rejection, HPA retargeting, image triggers and recovery; it reports partial qualification because supplemental scenarios remain outstanding.

The namespace cases exercise same-named applications across six namespaces, partial RBAC/manual failures, independent plans and isolated cutover/recovery. A regular 1Gi `gp3-csi` PVC from the preceding session is reused: a new independent canary is written from the source, read from the destination after migration, and read again after recovery. The original application marker, source spec, PVC UID and PV UID are preserved. This follow-up provisions no new volume or SCC grant.

Local validation passed: **148 Go race cases including subtests**, 74.2% statement coverage, `go vet`, vulnerability scanning with zero findings, security scanning with zero issues, 11 Python tests, formatting and five cross-builds. Cross-builds establish compilation only; the executed live artifact remains the recorded native binary.

## Scope and reproducibility

- Exact binary SHA-256: `f19a4c006e239b3c1a332f9aefc3c4ee5dc646dfba41e029a22634e98f0bb3f7` (`darwin/arm64`). Go source and public drivers: `12c0f15c5aa7e2531fc7b1fb573c0a2fb49d915a`.
- [Structured evidence](../compatibility/evidence/2026-10-16-preflight-4.21.36.json) records final case results, provenance, local validation and archive checksums.
- Credential-free archive: `.scratchpad/constraints-live-20261016/openshift-4.21.36-preflight-evidence.tar.gz`. It retains commands, stdout/stderr, original/staged/final snapshots, plans/journals, HTTP bodies, helper source and local test/scanner output. Every internal checksum is verified; kubeconfigs, credential files and actual admin/test secret values are excluded.
- Reproduce fresh scenarios with [the constraint runner](qualification.md#pre-retirement-constraint-driver), [new-app runner](qualification.md), and [multi-namespace runner](qualification.md#multiple-namespaces-in-one-invocation). Thirteen final constraint fixtures reuse previously created healthy isolated sources; only verified owned, paused, zero-replica destinations are regenerated for fresh plans. Three additional constraint fixtures are freshly created. The archived helpers record this distinction and prior fixture provenance.

The regression probes retain the earlier tool accepting Pod-rejected workloads and missing runtime-label policy differences. Earlier binary attempts, an HPA status resource-version race, transient API TLS timeouts and a baseline harness setup error remain historical failures; their successful cases are not relabelled as results for this binary. The HPA dry-run check now tolerates status-only churn while retaining UID/spec validation; the real deletion keeps UID/version preconditions. SCC review uses the workload's actual service-account username/groups, and raw Pod admission responses preserve labels and reject unsupported newer resource fields rather than silently dropping them.

The final overlap/PDB fixture intentionally remains at completed cutover with a healthy destination; an automatic stop-first rollback would interrupt its availability budget. Other successful migration fixtures recover their sources; refusal fixtures keep their original sources running. The temporary shared quota and lab SCC grant are removed. Completed superseded test namespaces were removed only after ownership verification; final evidence fixtures remain for inspection.

## Limits

Preflight is a point-in-time assessment, **not a capacity reservation or startup guarantee**. It fails closed when required inventory/dry-run access is unavailable or placement semantics cannot be evaluated safely. Nonzero migration now needs cluster-wide read-only node/Pod inventory and the additional namespace read/dry-run rights described in [RBAC](rbac.md). Rollback does not run this extra gate.

Image pulls, application readiness, generated-name/controller-identity-sensitive webhooks, custom scheduler plugins, CSI attachment/mounts, external networking and changes after assessment still require runtime verification and recovery. Capacity does not reserve future HPA scaling. NetworkPolicy checks detect changed selectors; they do not prove all traffic. Required inter-Pod affinity, hard topology spread and controller-relative overlap PDB budgets require manual review.

Only this native artifact and actual 4.21.36 were exercised live. The [compatibility matrix](../compatibility/matrix.json) remains partial: OpenShift 4.14, other minors, other native artifacts and outstanding supplemental release scenarios are unqualified. Existing [39-case new-app](new-app-live-tests.md) and [six-case namespace](multiple-namespaces-live-tests.md) sessions retain their original binary identities.
