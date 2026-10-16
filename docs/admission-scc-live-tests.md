# Admission fields and required SCC: actual OpenShift evidence

The admission/SCC candidate passed **two targeted live cases** and completed **13 automatic baseline scenario IDs** on **OpenShift 4.21.36 / Kubernetes v1.34.9**. Full qualification remains partial. Earlier binaries' results remain tied to their original hashes.

| Case | Observed result |
| --- | --- |
| Required-SCC authorization revoked after staging | A real `oc new-app --as-deployment-config` source remained healthy under `nonroot-v2` after its namespace-only use grant was removed. The subject review selected `restricted-v2`. The previous binary incorrectly passed read-only preflight; the current binary refused both preflight and cutover before changing the source, HPA or paused destination |
| Grant restored, migration and recovery | Restoring that namespace grant allowed preflight, actual cutover and explicit rollback. Source and destination Pods used the required SCC; source spec/HTTP and HPA spec recovered, destination paused at zero |
| Existing regular CSI volume | Separate actual cutover and rollback preserved the data canary, original source spec/HTTP and existing PVC/PV identities, without creating new storage |

The first two rows are phases of one targeted authorization case, not independently inflated test counts. The second targeted case is the regular-volume migration.

The required-SCC fixture uses one service account, a namespace Role permitting only `use` of the existing `nonroot-v2` SCC, and its RoleBinding. The SCC was checked to forbid privileged containers and host access. Its captured users/groups contain no direct grant for this fixture identity, so deleting the test binding genuinely removes the relevant authorization. No SCC definition or ClusterRole is created or modified. The final source and namespace grant remain available for inspection in `dcmig-admission-20261016-required-scc`.

During both refusals, source Pod UID `46925616-1f4c-41b3-9418-af2d310799f5` stayed healthy with the expected HTTP marker. The source DC UID/spec, original HPA UID/spec and staged destination UID/spec were preserved. After authorization was restored, the Deployment ran Pod `0f6ffa2e-8b93-444c-a59e-ec35f862f921`; verified rollback restored source Pod `f7b44929-e833-456d-b4c2-b19e51f101f9`. Recovery rerun also passed. The previous binary was used **only for read-only preflight**, never for an unsafe cutover.

## Raw admission fields and compatibility scope

Actual ephemeral SCC reviews and Pod server dry runs in the existing baseline namespace returned specs fitting the 1.27 field catalog. Both ordinary input and `hostUsers: false` selected `restricted-v2`; no `restricted-v3` runtime qualification is claimed. These probes persist no Pod and are separate from the two migration cases.

Local request-contract regressions first reproduced acceptance of admission-added image volumes, `supplementalGroupsPolicy` and `seLinuxChangePolicy` at both SCC/Pod boundaries, for preflight/cutover. The new guard validates the complete raw spec before typed decoding or resource/placement assessment. These are simulated injection cases, not a claim that a live mutating webhook injected those fields. Required-SCC regressions additionally cover denied/uncertain authorization, unavailable review permission/API, wrong policy/defaults, lost annotations and preservation of ordinary namespace permissions.

LocalSubjectAccessReview and its user/groups/resource attributes were verified against the official Kubernetes **v1.27.16** source. It is used only when the SCC subject review selected a different policy from the required annotation. The actual Pod dry run then applies the pinned policy to the original template after service-account authorization; defaults from an unrelated SCC are not replayed. Legacy direct SCC users/groups assignments may need manual/RBAC review when they are not proved by the selected subject review; see [permissions and behavior](rbac.md).

## Provenance and remaining qualification

- Native artifact SHA-256: `910d09ff213e33aca25008ddce7808cdfd40d9d594803dc44f2a5c764b8a55c3`, `darwin/arm64`.
- Production source: `618831fe76f52bb382ea0163ebfd74d761927cb9`; raw-field guard `278d942`, required-SCC fix `618831f`.
- Live driver revision: `1d5391e`. A later fixture precondition (`be9f1d3`) excludes direct SCC grants; the captured initial SCC was audited and already satisfies it. Exact tested/current driver snapshots are retained separately.
- Validation: **212 Go race cases including subtests**, **75.6%** statement coverage, `go vet`, **20 Python tests**, zero vulnerability findings, zero security issues/compiler errors, and five platform builds. Builds establish compilation only.

[Structured evidence](../compatibility/evidence/2026-10-16-admission-scc-4.21.36.json) retains cases, actual authorization responses, previous-artifact comparison, source/driver hashes, raw-field probe scope, baseline results and limits. The baseline result remains `partial` and exits nonzero because supplemental records were not imported; all 13 automatic scenario IDs completed without a recorded scenario error.

The private archive `openshift-4.21.36-admission-scc-evidence.tar.gz` contains 339 files, SHA-256 `9f43d3ba1b3e230fa6ea7f2bce33cb247585c59d5b07b490bd1d1805b0673392`; manifest SHA-256 `b8585101a1be66af3f9bf2680b49221ebff7871b60e3648a7911b3fa26c6a7ff`. Every archived checksum was verified, actual credential values were checked in memory and excluded, and the complete source snapshot rebuilds byte-for-byte to the tested native binary. Archive/rebuild metadata were appended to the public record after packaging to avoid circular checksums.

This session reuses the approved existing 1Gi gp3 CSI volume and a verified empty baseline namespace. It does not qualify cross-node storage, node failure or every optional policy. Other exact-artifact recovery, image, RBAC, singleton and namespace variants remain outstanding. The [4.14–4.20 review](compatibility-review.md) is still in progress, including raw Deployment/HPA response boundaries; no older-version compatibility assumption is marked complete.
