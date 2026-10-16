# Maintained-client migration qualification

The aligned Kubernetes v0.37.1 clients have been tested on **OpenShift 4.21.36 / Kubernetes v1.34.9**, using the native Darwin arm64 binary built from `7eac1da`. Its SHA-256 is `b2472a02d402fe88435483080f5137673d486cc015d97c82cc439126169a28a8`. The [structured result](../compatibility/evidence/2026-10-16-client-upgrade-4.21.36.json) records the completed scope and evidence archive. Other binaries' results remain historical.

## Workloads and failure handling

| Area | Observed coverage |
| --- | --- |
| Actual `oc new-app --as-deployment-config` | 40 cases, including local/external/cross-namespace images, grouped containers, configuration/Secrets, labels, metadata options, multiple Services and independent applications |
| Builds | Docker and S2I binary builds, followed by rebuilds after cutover; ImageStream output continued updating the Deployment while the DC remained retired |
| DC semantics | Replica counts 0/1/3, Recreate, Rolling, zero surge, overlap, init containers and long names; test, paused, manual-trigger, hooks and custom-deployer cases correctly require intervention |
| Controller and dependency safety | Stale source/Service, changed target, foreign name/selector, newly added HPA, disabled image trigger, volume overlap and blocked traffic |
| Runtime failures | Initial destination command failure, failed image-triggered rollout, orphaned destination Pod, explicit rollback and safe reruns |
| Multiple namespaces | Six batch/isolation cases across six namespaces; three same-named apps with replicas 1/2/0, distinct HTTP responses, a sentinel, a manual source and an empty namespace |
| Access | Restricted namespace credentials, denied DC listing/create/update/HPA operations, actual quota admission rejection and actual server rejection of an expired signed token |
| Singleton worker | Success, startup failure and cancellation; sampled Pod ownership and Lease heartbeats during cutover and recovery |
| Existing regular CSI volume | Original PVC/PV identities retained, original application marker preserved, new marker readable before/during/after migration and rollback |

These are test cases with expected outcomes. Unsupported configurations pass their test by refusing automatic migration. They are not successful workload migrations.

The regular-volume test reused the approved 1Gi CSI claim. It provisioned no storage and required no additional SCC grant. All 99 volume-ownership samples completed without read errors or concurrent source/destination volume Pods. Worker samples likewise recorded no concurrent workers or read errors. Sampling and source-drain checks establish the observed transitions; they do not prove every possible instant, cross-node attachment behavior, every CSI driver or arbitrary worker implementations.

The credential test waited for server enforcement. Five requests were accepted after the token's declared expiry before authentication rejection was observed; the test did not infer rejection from the timestamp. The failed operation produced a nonzero exit and failed report, with the source UID/spec/HTTP unchanged and no destination created.

## Preserved unsuccessful attempts

The first namespace isolation cutover was refused for insufficient actual cluster Pod capacity before source or HPA mutation. After completed fixtures were snapshotted and retired, the existing plans passed cutover and recovery in different orders. Other namespaces' source/target states and HTTP responses were checked after each step. The original refusal remains in the evidence.

Lost-response faults were injected after each of six successful persistent mutations. Five cases recovered on the first recovery attempt. The fifth case's initial recovery failed visibly on a Deployment resource-version conflict. Two explicit rollback invocations then succeeded; independent API/HTTP inspection verified the original source and HPA specs, stopped destination and rolled-back journal. A helper error in the final HTTP observation was also retained and followed by successful independent verification. This establishes verified eventual recovery for all six faults, with an explicit rerun required for the fifth case.

## Constraints and release limits

All 16 constraint cases passed. Thirteen negative cases each verified both preflight and cutover refusal, unchanged source availability/spec/HTTP and HPA UID/spec, a paused zero-replica destination and a still-staged journal. These cover namespace and shared cluster quotas, ReplicaSet admission, LimitRange defaults, node selection/capacity, namespace/runtime-label NetworkPolicies, disruption budget, missing service account, SCC grant revocation and a constraint changed after successful preflight. Positive, zero-replica/zero-quota and justified PDB-preserving overlap cases passed.

Additional regressions passed six ANP/BANP selector variants (12 refusals within one aggregate case), raw persisted Deployment-field refusal with lossless explicit recovery, required-SCC grant revocation/restoration, and four PV topology-label variants (eight refusals). Source/HPA/staged-target preservation and positive migration/recovery were checked where applicable. Temporary PV labels and test cluster policies were restored/removed. The required-SCC test retains only its namespace-scoped binding to an existing SCC; it creates no SCC or ClusterRole.

Test resources use labelled disposable namespaces. Completed fixtures may be retired after UID/ownership checks and snapshots to keep the cluster below its Pod limit. The regular PVC and representative fixtures remain available; removed fixtures' logs and snapshots remain in the private archive. Temporary cluster policies are removed by their verified identities. Credential files, kubeconfigs and executables are excluded from the archive, and exported files are scanned against actual credential values.

Local validation passed 356 race tests/subtests (79 top-level tests), 77.3% statement coverage, vet, module verification, an empty tidy diff, 24 Python tests, and security scans with no findings. Four additional platform builds compiled; they were not executed against the cluster. Minimum-server contract tests cover JSON reads/writes even with ambient CBOR flags enabled, rejection of CBOR responses and baseline toleration matching.

OpenShift 4.14–4.20 use the explicitly approved [source-review compatibility assumption](compatibility-review.md), bound to this production/dependency/fixture set. They have no actual integration pass. OpenShift 4.22 and the other shipped platform binaries still require actual qualification. No release or publication is authorized by this report.

## Evidence archive

The private archive is `.scratchpad/client-upgrade-20261016/openshift-4.21.36-client-upgrade-evidence.tar.gz` (SHA-256 `476b644be9f39ebe0d318ad5fd08a022210589d2af518abee5fdd0d656019618`). Its 5824 files have a verified SHA-256 manifest; exported files contain none of the actual admin/test credential values checked by the packaging scan. Rebuilding its saved source with the original flags reproduces the live-tested binary byte for byte. Archive/rebuild metadata is appended to the public record after sealing to avoid circular checksums.
