# Actual multi-namespace migration tests

All **six multi-namespace cases passed** on **OpenShift 4.21.36 / Kubernetes v1.34.9**, against the same native binary used by the [39-case new-app session](new-app-live-tests.md). This follow-up tests a single CLI invocation with several `--projects`, and isolates actual migration/recovery across three namespaces. It creates six dedicated labelled namespaces and five actual `oc new-app --as-deployment-config` apps, all named `web`.

| Namespace | Purpose | Original replicas |
| --- | --- | --- |
| `dcmig-ns-final-20261016-a` | Migration target, distinct HTTP response | 1 |
| `dcmig-ns-final-20261016-b` | Migration target, distinct HTTP response | 2 |
| `dcmig-ns-final-20261016-c` | Migration target, remains scaled to zero | 0 |
| `dcmig-ns-final-20261016-sentinel` | Unselected application must remain unchanged | 1 |
| `dcmig-ns-final-20261016-manual` | Unsupported test DC must remain manual | 1 |
| `dcmig-ns-final-20261016-empty` | No DeploymentConfigs | — |

## Results

| Case | Result | Evidence |
| --- | --- | --- |
| Batch generation | passed | One invocation handles three namespaces with the same DC name; separate output directories and source identities. A duplicate project is processed once; one empty namespace and one reserved namespace have explicit empty/skipped entries. |
| Batch live planning | passed | One invocation creates three independently identified, namespace-bound plans and records the empty namespace. |
| Batch-plan server dry runs | passed | Each of those three plans is validated without persisting a Deployment; source specs remain unchanged. |
| Mixed unsupported workload | passed | Generation and planning each return a nonzero aggregate exit while preserving the manual result and continuing through later healthy/empty namespaces. |
| Mixed namespace permissions | passed | A short-lived reader can list DCs in A/C and is denied B; one invocation generates two outputs, reports one permission failure, then processes C successfully. |
| Cutover/recovery isolation | passed | Stage/rerun all three; cut over A → B → C, recover B → C → A; repeat completed operations. Verify all three namespaces' source/destination state and distinct active HTTP responses after every step. The sentinel and manual sources remain unchanged. |

Three destination UIDs and migration IDs are distinct. A post-run snapshot audit also verifies Service UID/spec and ImageStream UID retention in all five app namespaces, plus the three restored source specs and paused zero-replica destination identities. The source replica counts recover to 1/2/0.

Generation and planning accept several projects in one invocation. Dry run, stage, cutover and rollback execute one reviewed plan at a time. Execution plans bind the health URL to each application's own Route; the checks also verify its distinct HTTP response. No batch mutation mode was added.

## Exact artifact and retained evidence

- Binary SHA-256: `b21b39c83941a3944c528387293e4c3355fb57137fc1f1874cc452c237b370fb` (`darwin/arm64`, Go source `050cfcc891dbe8491f59c8ddb5f3ee9ab91fe5f2`). No converter Go source or binary changed for this follow-up.
- Exact executed driver SHA-256: `bd6ef3f107a85721b2901798d232a15a5c9c41706a95e2d8bfc31cb5d98bad95`; identical content is committed at `ee8f6a9a50d7f73ac363ea5c21c42676cd2781f7`. The invocation began on the recorded preceding revision with this new driver uncommitted; the archive retains the exact source and provenance rather than relabelling that base revision as containing the driver.
- [Structured evidence](../compatibility/evidence/2026-10-16-multiple-namespaces-4.21.36.json) contains case observations, all namespace names, per-project report totals, UIDs, provenance and archive checksums.
- Credential-free archive: `.scratchpad/multiple-namespaces-live-20261016/openshift-4.21.36-multiple-namespaces-evidence.tar.gz`. Command inputs/stdout/stderr, source/staged/final snapshots, plans/journals, HTTP bodies, partial-failure reports and the original failed driver attempt are retained. Every internal checksum was verified. Actual admin/test tokens and keys, credential profiles and kubeconfigs are excluded.

The first driver attempt treated an omitted empty `blockers` field as required and passed an unsupported `opener` keyword to `pathlib.Path.open`. These harness errors also prevented its dependent dry-run check; they remain recorded as failures. The corrected full suite passed in six fresh namespaces. The original attempt's isolation check also passed and verified recovery before its six ownership-verified namespaces were removed. Final namespaces remain for inspection. This suite provisions no volume, node, StorageClass or cluster role; reader permissions are namespace-scoped.

Nine local Python tests, Black and diff checks pass. The preceding session's 119 Go race cases, vet, security scans and five cross-builds apply to the unchanged converter source/binary. This actual 4.21.36 subset does not qualify 4.14, other minors, other native artifacts or the full release matrix. Reproduce with [the multi-namespace runner](qualification.md#multiple-namespaces-in-one-invocation).
