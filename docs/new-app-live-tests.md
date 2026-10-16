# Actual oc new-app migration tests

All **39 targeted assertions passed** on **OpenShift 4.21.36 / Kubernetes v1.34.9**, using real `oc new-app --as-deployment-config` applications. All **13 automatic baseline scenarios** also passed on the same final binary. Runs began October 15 locally and completed October 16 UTC. Expected refusals count as passes only after checking the failure and relevant resource state; they do not mean the unsupported workload migrated.

The user authorized resource CRUD on this throwaway admin-connected cluster and requested a regular volume. Tests created labelled, isolated namespaces. Docker/S2I builds used the existing registry; no cluster, node, registry or StorageClass was provisioned. One final 1Gi dynamically provisioned gp3 CSI test volume remains for inspection. The initial duplicate claim/PV was removed and its absence verified.

## Exact artifact and raw evidence

- Native artifact: `darwin/arm64`, Go 1.27.2.
- Binary SHA-256: `b21b39c83941a3944c528387293e4c3355fb57137fc1f1874cc452c237b370fb`.
- Go source revision: `050cfcc891dbe8491f59c8ddb5f3ee9ab91fe5f2`; runner revision: `485770e97ef0850b74395270c2a973b6eb5693d2`.
- Kubernetes libraries remain aligned at `v0.31.3`.
- [Structured results](../compatibility/evidence/2026-10-16-new-app-4.21.36.json) record source/destination identities, image/build behavior, storage observations, dependency/fixture hashes and all case results.
- Raw evidence: `.scratchpad/new-app-live-20261015/openshift-4.21.36-new-app-evidence.tar.gz`, containing command inputs/stdout/stderr, actual source snapshots, server dry runs, staged objects, plans, journals, readiness/routing checks, build inputs/output, admitted Pods and the volume ownership timeline. Its SHA-256 and file count are recorded in the structured results. Every internal `SHA256SUMS` entry was verified after packaging. Kubeconfigs, actual credential values and executable binaries are excluded.

## Actual cases

| Case | Result | Verified behavior |
| --- | --- | --- |
| plain | passed | Unmodified image-based new-app |
| env-labels | passed | Environment and application labels |
| metadata-off | passed | Optional metadata copying disabled |
| local-image | passed | Local ImageStreamTag update |
| cross-image | passed | Cross-namespace ImageStreamTag update |
| grouped | passed | Two new-app components grouped in one DC |
| config-secret | passed | ConfigMap/Secret mounts and environment references |
| multiple-services | passed | Multiple Services; HTTP on 8080 and 8888 |
| replicas-three | passed | Three replicas |
| scaled-zero | passed | Explicit zero replicas |
| recreate | passed | Recreate strategy |
| zero-surge | passed | Rolling with zero surge |
| overlap | passed | Explicit overlap cutover |
| init-container | passed | Init container and shared volume |
| long-name | passed | Long valid resource name |
| test-app | passed | Test DC refused |
| paused | passed | Automatic activation of paused source refused |
| manual-triggers | passed | Disabled ConfigChange refused |
| rolling-hook | passed | Rolling hook refused |
| new-pod-field | passed | Post-1.27 Pod field refused |
| stale-source | passed | Source changed after plan refused |
| stale-service | passed | Service changed after plan refused |
| regular-pvc | passed | Regular CSI PVC; marker preserved; no sampled overlap |
| docker-build | passed | Docker binary build and post-cutover rebuild |
| s2i-build | passed | S2I binary build and post-cutover rebuild |
| unbuilt-binary | passed | Review allowed; unresolved live image migration refused |
| template | passed | Actual processed new-app Template |
| changed-target | passed | Changed staged Deployment refused; explicit recovery |
| foreign-name | passed | Foreign destination retained unchanged |
| foreign-controller-overlap | passed | Overlapping foreign controller retained unchanged |
| blocked-health | passed | Denied destination traffic fails health; recovery verified |
| two-apps | passed | Two apps sharing app label migrate independently |
| disabled-image | passed | Disabled image trigger ignores imported tag change |
| added-hpa | passed | HPA added after planning refused |
| volume-overlap | passed | Volume-bound overlap refused |
| failed-image-rollout | passed | Wrong-port image fails readiness; recovery verified |
| recreate-hook | passed | Recreate hook refused |
| custom-deployer | passed | Custom deployer refused |
| legacy-pending-plan | passed | Pre-fix saved plan refused for stage and cutover |

Supported cases verify source readiness, live planning, nonpersistent server dry run, paused zero-replica staging, stage rerun, cutover, completed rerun, rollback and recovery rerun. Service UID/spec, ImageStream identity, BuildConfig UID/spec where present, and pod-facing labels are retained. HTTP checks accompany Service endpoint checks for active workloads; the zero-replica case verifies zero instead of expecting traffic.

The two-app case stages both new-app sources with a shared `app` label and distinct Service selectors, then migrates and restores each independently. The other source remains unchanged. Grouped components, init containers, ConfigMap/Secret references and both exposed HTTP ports are exercised with actual Pods.

## Build and regular-volume evidence

Both the Docker and S2I cases completed a real initial binary build, migrated the resulting DC, and rebuilt while the Deployment served traffic. New build output updated the Deployment image and HTTP response; the old DC stayed at zero with triggers removed. Restoring the initial ImageStreamImage allowed verified rollback. The BuildConfig, ImageStream and Service identities survived. Inputs and both build logs are retained.

The regular-volume case used a **1Gi ReadWriteOnce PVC**, the existing **gp3-csi** StorageClass and **ebs.csi.aws.com** driver. Source, destination and recovered source read the same marker `new-app-pvc-1ebfa05f`. PVC UID `bca86984-9c1c-4b98-8a73-85b698683560` and PV UID `356e7dcb-4a0e-4a8b-9a17-c82ecc5e5ad7` remained unchanged. Actual Pod admission used **restricted-v2**; no custom SCC binding was created. Across **100 ownership samples**, no simultaneous source/destination Pod or observation read error was recorded. Sampling supplements the journal and cannot observe every instant.

The older static hostPath attempt failed its source marker precondition under SELinux and remains recorded as failed in the [earlier evidence](live-tests-2026-10-15.md). The regular PVC superseded it. Its namespace, legacy SCC binding and PV definition were removed; no `hostmount-anyuid-v2` binding was applied. No further node-directory modifications were made.

## Defects corrected and retained failures

- `2d86986`: a new-app binary build awaiting its first image had no `lastTriggeredImage`, yet the old live planner/stager accepted it. A failing regression reproduced the gap. Live plans, stage and cutover now reject unresolved automatic image triggers with an actionable diagnostic. Read-only review generation and rollback remain available. A real plan saved by the old binary is rejected by the final binary without changing source or destination.
- `050cfcc`: if a later health request reached the deadline, it could hide an earlier observed HTTP failure. The diagnostic now retains the observed error and deadline; a deterministic regression passes.
- Initial harness attempts exposed asynchronous ImageStream imports and an inappropriate HTTP precondition for a zero-replica source. Setup was corrected, failures retained, and all cases rerun in fresh namespaces on the final binary. The unbuilt-app test was corrected to allow read-only review while requiring live refusal. A baseline attempt before creating its required labelled namespace failed preflight and is retained separately; the properly prepared run passed all 13 automatic scenarios.

## Validation and scope

**119 Go cases including subtests** pass with race detection; statement coverage is **77.8%**. Vet, eight Python tests, formatting, actionlint and module verification pass. Govulncheck reports zero symbol-level findings; gosec reports zero issues and compiler errors. Final source builds for Linux amd64/arm64, Darwin amd64/arm64 and Windows amd64. Only the recorded Darwin arm64 artifact was exercised live.

This session passes the listed assertions, while full release qualification remains **partial**. OpenShift 4.14, the other required minors/platform artifacts, all admission-policy variants, cross-node CSI detach/attach and node failure remain unqualified. Earlier actual tests use their own recorded binary hashes and are not relabelled as results for this artifact. The automatic baseline remains partial because it has no imported supplemental variant records. The release gate continues to require the complete matrix; no push or release was performed.

Final labelled test resources remain for inspection. Successful supported cases end with the recovered source and a paused zero-replica destination. Intentionally manual and foreign-object cases retain their documented states. The baseline driver cleans up its own labelled resources after successful checks. Reproduce the matrix with [the new-app runner instructions](qualification.md#apps-created-with-oc-new-app).

The [multi-namespace follow-up](multiple-namespaces-live-tests.md) additionally passes six cases across six dedicated namespaces, including multi-project invocations, mixed RBAC/manual failures and isolated cutover/recovery of three same-named apps.
