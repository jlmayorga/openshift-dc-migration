# Linux execution and live migration tests

The later [source-conflict retry follow-up](source-conflict-live-tests.md) changes production Go code. Its current Linux/amd64 artifact `cdbd5358…` passed seven targeted conflict/recovery cases and three regular CSI migrations. The full 22-group result below remains specific to historical artifact `4297f76e…`; it is not transferred to the newer executable.

The Linux/amd64 candidate from `920dfc0` passed all **22 required scenario groups** on **OpenShift 4.21.36 / Kubernetes v1.34.9**. Its SHA-256 is `4297f76e3bb20a0d804cea86220aa82b288ba810920ee4b44d1d27d74e9f67f7`. The [structured full result](../compatibility/evidence/2026-10-16-linux-full-4.21.36.json) binds the exact executable, source, dependencies, fixtures, cluster release image and observed results. The [earlier complete Darwin arm64 run](client-upgrade-live-tests.md) remains a separate artifact result.

| Actual Linux coverage | Evidence |
| --- | --- |
| Baseline migration | Schema, dry run, SCC admission, overlap/exclusive cutover, Service routing, HPA retargeting, stale plans, constraint refusal, interruption, reruns, rollback and image-controller updates |
| Image variants | Cross-namespace, disabled and multiple-container image triggers, with updates and rollback |
| Permission/admission refusal | Restricted generation/staging/cutover credentials, quota admission, foreign destination and denied HPA deletion; refusal state checked |
| Lost write responses | Fault injected after each of six persistent mutations; explicit recovery and rollback reruns verified |
| Singleton worker | Success, destination startup failure and actual cancellation; Pod/Lease ownership samples, source drain and source recovery verified |
| `oc new-app` runtime variants | Failed initial rollout and orphan recovery, plus the existing regular-CSI application; the native run's 40-case matrix is not attributed to Linux |
| Regular CSI volume | Same original PVC/PV UIDs; application marker and new canary retained before/during/after cutover and rollback; 84 Pod samples, no read errors or observed simultaneous owners |

The tests used multiple isolated namespaces and an explicitly approved temporary account. It received namespace creation and read-only cluster inventory through the exact reviewed ClusterRole/Binding; a host broker granted workload fixture permissions only in verified test namespaces and the UID-verified existing CSI namespace. No admin credential entered the runner, and no new storage or SCC change was used. The temporary global and namespace grants, runner Pod and account were removed after qualification; per-object UID checks and absence observations are retained in the structured result and raw archive.

Initial failures remain in the evidence. These include missing metadata tooling, a wrapper import error before baseline API calls, overly broad child fixture permissions, actual exhaustion of the node's 250 Pod slots, and resource-version conflicts. Completed fixtures were retired only after snapshots and identity/state checks. Failed fixtures were recovered or verified before retirement. The least-privilege HPA fixture correction is committed separately as `5f667aa`; the executed converter and frozen source checkout remain `920dfc0`.

The CSI pass required four attempts. Each failed attempt completed rollback and full source/PVC/destination verification before a fresh plan. A cancellation attempt also encountered a conflict after DC triggers had been quiesced: its original Pod was still serving, but full source-spec restoration required explicit rollback. Both that recovery and a separate fresh cancellation pass are retained. A read-only dry-run error does not imply earlier migration steps made no changes; follow the journal and recovery procedure. The CLI does not blindly retry uncertain writes.

Diagnostic Event reads were denied to this test account; those errors are retained. Scenario assertions use migration reports, HTTP/API observations, Pod/Lease samples and permitted resource snapshots. Ownership sampling supplements the source-drain checks; it does not prove every instant, every worker implementation, cross-node behavior or every CSI driver. Point-in-time preflight cannot reserve quota/capacity or guarantee startup after concurrent changes.

The earlier [namespace-only Linux staging result](../compatibility/evidence/2026-10-16-linux-stage-4.21.36.json) remains historical: 356 actual Linux unit/contract tests, nine offline version fixtures and generation/planning/dry-run/staging checks on artifact `f042f411…`, with no cutover pass inferred from that earlier run. Its archive is unchanged (`d49a77bc1c4d84cb8b9bf3e7f3c55870d488d1081360af550eba43abef0f72e7`).

OpenShift 4.14–4.20 have source-bound review assumptions, not actual integration passes. OpenShift 4.22 and the remaining shipped platforms still lack matching full qualification. **The release gate remains closed.**

The private archive `.scratchpad/linux-full-20261016/openshift-4.21.36-linux-full-evidence.tar.gz` has SHA-256 `3dc69087c05d43ace194eef135edf5aaa437e1560ec2e457e64b96d30ab21e34`. All 3756 manifest entries verified. Actual credential values were checked before export and sealing. Rebuilding its saved source reproduces the executed Linux binary byte for byte. Archive/rebuild metadata was appended after sealing to avoid circular checksums.
