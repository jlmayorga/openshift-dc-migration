# Linux execution and live migration tests

The current **Linux amd64** migration binary passed all **22 required scenario groups** on **OpenShift 4.21.36 / Kubernetes v1.34.9**. Its source revision is `eb8fb9d6be6274f059211c913191345ab25a9cb1` and SHA-256 is `cdbd5358de223a8120e5defdb415813e54250b0d368a3d02455cce4f5913e02c`. This includes the bounded source-conflict retries and protection against implicit SDK retries of uncertain writes. The [structured full result](../compatibility/evidence/2026-10-16-linux-current-full-4.21.36.json) binds the exact binary, source, dependencies, fixtures, cluster release image and actual outcomes.

The suite executed inside a Linux x86_64 Pod using the dedicated, limited test account. It ran **381 unit/contract tests and subtests (83 top-level tests)**, then fresh integration variants and the canonical qualification. Linux unit execution was without the race detector; the separate 381-test Darwin arm64 race run applies to the same unchanged production source. Fake API and offline unit results are not counted as OpenShift integration.

| Fresh actual Linux coverage | Verified outcomes |
| --- | --- |
| Baseline qualification | 13 automatic scenario groups: schema, dry run, SCC admission, exclusive/overlap cutover, routing, HPA retargeting, stale plans, constraint refusal, interruption, reruns, rollback and image-controller updates |
| Image variants | Cross-namespace, multiple-container and disabled image triggers; updates and rollback |
| Permission/admission refusal | Four cases: restricted credentials, quota admission, foreign destination and denied HPA mutation; source and refusal state checked |
| Lost write responses | All six persistent mutation points; failed/uncertain results followed by explicit rollback and recovery reruns |
| Singleton worker | Success, startup failure and cancellation; source drain, HTTP and Pod/Lease samples checked |
| New-app runtime | Actual `oc new-app --as-deployment-config` applications with failed initial rollout and orphan recovery |
| Regular CSI volume | Original source/PVC/PV UIDs and application marker preserved; fresh data canary read before/during/after migration and rollback |
| Multi-namespace extension | Six additional cases: batch generation, planning, dry runs, manual/RBAC partial failures and isolated cutover/recovery across three same-named apps with 1/2/0 replicas, plus sentinel/manual/empty namespaces |

The 22-group result combines the 13 baseline groups with nine reviewed, fresh, same-binary actual supplemental records. No native or earlier-binary pass was imported. The new CSI run retained 76 Pod samples, with zero read errors and no observed simultaneous volume owners; it handled one natural source resource-version conflict with an automatic retry, without restarting the CLI. Ownership sampling supplements source-drain checks and cannot prove every instant, cross-node behavior or every CSI driver. Point-in-time preflight cannot reserve capacity or guarantee application startup.

Tests used multiple isolated namespaces and two concurrent fixture jobs to stay within the existing node's Pod capacity. Passed fixtures were retired only after snapshots, namespace UID/label checks and state verification. The six-namespace extension also verified complete source specs and paused zero-replica destinations before retirement. The original CSI storage and earlier staging workload were preserved. No new storage or SCC modification was used.

The exact previously approved temporary ClusterRole/Binding supplied namespace creation and read-only cluster inventory. A host broker granted workload permissions only in verified test namespaces and the UID-verified existing CSI namespace. No admin credential entered the Pod. All temporary global/namespace grants, runner and account were removed or verified absent, checking 58 tracked object identities. Actual credential values were scanned before export, and the downloaded archive's complete manifest verified.

Diagnostic Event reads were denied to this limited account; those errors remain in the raw evidence. Assertions use permitted resource state, migration reports, HTTP and Pod/Lease observations. A private image-feature fixture adapter waits for the destination controller's resource version to settle before rollback; it adds no production retry behavior.

The [earlier source-conflict session](source-conflict-live-tests.md) separately records seven targeted Linux conflict/uncertain-response cases and three CSI repetitions on this identical binary. Its partial session record is preserved; the new full suite now supplies the missing Linux qualification. The older [4297 full result](../compatibility/evidence/2026-10-16-linux-full-4.21.36.json) and [namespace-only staging result](../compatibility/evidence/2026-10-16-linux-stage-4.21.36.json) remain historical results for different binaries, including their failures and recoveries.

| Target | Current actual integration status |
| --- | --- |
| 4.21.36, Darwin arm64 | All 22 groups passed on recorded native artifact `158b3523…` |
| 4.21.36, Linux amd64 | All 22 groups passed on current artifact `cdbd5358…` |
| 4.21, Linux arm64 / Darwin amd64 / Windows amd64 | Untested; cross-compiled only, without native platform execution or live qualification |
| 4.22, every platform | Untested; no authorized cluster available |
| 4.14–4.20 | Source-bound review assumptions with prerequisites; no actual integration pass |
| Next development release | Untested; no authorized development cluster available |

**The full release gate remains closed.** GitHub workflow execution and publication are not claimed. Qualification applies to the recorded candidate hashes; a release build with different source or build metadata requires evidence for its exact binaries. See the [release checklist](qualification.md).

The private raw archive is `.scratchpad/linux-current-full-20261016/openshift-4.21.36-linux-current-full-evidence.tar.gz`, SHA-256 `8a5f7fa9de07873408b2075a6b046d0c2e851d05444aeda22693b631131bf080`. All 2400 manifest entries verified. Its exported source rebuilds the tested Linux executable byte for byte. Archive metadata was appended after sealing to avoid circular checksums.
