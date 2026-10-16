# OpenShift 4.14–4.20 compatibility review

**Status: in progress; no review-based compatibility assumption has been approved by this assessment yet.** Older clusters are unavailable. The agreed alternative is to review official release notes against the actual tool, address applicable breaking changes, then record an explicit assumption for the reviewed range. This is separate from actual OpenShift integration results. Review started on 2026-10-16; see the [backlog and acceptance criteria](backlog.md).

The first pass found runtime storage changes despite continued availability of their API fields. Further review found raw admission-field loss/unassessed semantics and required-SCC authorization that a different subject-review policy could not establish. [Storage/admission guards](feature-mapping.md) and an [actual SCC revocation regression](admission-scc-live-tests.md) now address these paths. The remaining admission boundaries, scheduling/accounting, controller, client-library and patch-advisory review still needs completion. Do not treat collecting a release's notes or establishing its Kubernetes version as a completed compatibility assessment.

## Version mapping and source provenance

Each mapping below comes from that release's official “About this release” text, rather than extrapolating version numbers. Red Hat's HTML pages exceeded the browser fetch limit and direct retrieval returned HTTP 403. The review therefore uses immutable revisions of the official `openshift/openshift-docs` release-note source, including referenced release-note and z-stream modules. Upstream changelogs were independently re-fetched at Kubernetes commit `aea561709867d4039260bd462cb456b9b46906a1` and matched the downloaded hashes for all seven versions.

| OpenShift | Kubernetes | Official release-note source | Assessment |
| --- | --- | --- | --- |
| 4.14 | 1.27 | [4.14 source](https://github.com/openshift/openshift-docs/blob/9e1779093c4cacdc324992e84c0b03900a3a9ffe/release_notes/ocp-4-14-release-notes.adoc) | Mapping verified; full assessment pending |
| 4.15 | 1.28 | [4.15 source](https://github.com/openshift/openshift-docs/blob/ed0612e21c09eafcc9492f79195fa38254c3e1f9/release_notes/ocp-4-15-release-notes.adoc) | Mapping verified; full assessment pending |
| 4.16 | 1.29 | [4.16 source](https://github.com/openshift/openshift-docs/blob/d8ed17c431a59e24ff889c58eb03d8d4e5068d70/release_notes/ocp-4-16-release-notes.adoc) | Mapping verified; full assessment pending |
| 4.17 | 1.30 | [4.17 source](https://github.com/openshift/openshift-docs/blob/6c0902cc63abcac6b44a6467ca42be285ec01aad/release_notes/ocp-4-17-release-notes.adoc) | Mapping verified; full assessment pending |
| 4.18 | 1.31 | [4.18 source](https://github.com/openshift/openshift-docs/blob/e3d56cca51bcae6fd417bde29798512fa5f40723/release_notes/ocp-4-18-release-notes.adoc) | CephFS/RBD removal addressed; remaining review pending |
| 4.19 | 1.32 | [4.19 source](https://github.com/openshift/openshift-docs/blob/eafb4a5cfd576aba7dd83307dd121274a52c4ab4/release_notes/ocp-4-19-release-notes.adoc) | Earlier storage guard applies; remaining review pending |
| 4.20 | 1.33 | [4.20 About this release](https://github.com/openshift/openshift-docs/blob/08bc31a85b65f871838334f1cfd911ef08e77f69/modules/rn-ocp-release-notes-about-this-release.adoc) | `gitRepo` default change addressed; remaining review pending |

The [structured review record](../compatibility/evidence/2026-10-16-release-notes-review.json) preserves source revisions, hashes, inventory and unfinished items. Retrieved notes and 274 referenced modules are retained privately for reproducible analysis; a successful fetch is not a review result. This initial record does not establish that every relevant advisory or referenced guide has been assessed.

## Production API and operation inventory

This inventory follows `runtime.go`, `converter.go`, `baseline.go`, `planning.go`, `application.go`, `cutover.go`, `constraints.go`, `quota.go`, `scheduling.go`, `storage.go` and `scc.go`. The code graph and path coverage were checked, with source reads for material behavior. The baseline field catalog is generated from Kubernetes 1.27.16 OpenAPI. Network traffic uses JSON; optional APIs are discovered when needed.

| API | Resources and use |
| --- | --- |
| `apps.openshift.io/v1` | DeploymentConfigs: list/get, preserve source snapshot, dry-run and actual updates to triggers/replicas/pause/selector, explicit source restoration |
| `apps/v1` | Deployments: get, dry-run/actual create/update; ReplicaSets: list, representative create dry run, ownership/termination checks; StatefulSets/DaemonSets: controller overlap inventory |
| `batch/v1` | Jobs: controller overlap inventory; production does not create replacement hook Jobs |
| core `v1` | Pods: namespace and cluster inventory, raw create dry run, readiness/ownership/termination and allocation checks; RCs: source ownership/retirement; Services: inventory/read; Endpoints: traffic verification; ResourceQuotas/LimitRanges: read; PVCs/PVs: bound storage/topology/plugin checks; Nodes: placement/capacity inventory |
| `autoscaling/v2` | HPAs: raw baseline validation plus typed reads/list, dry-run/actual delete/create for temporary freeze and retarget/restoration |
| `networking.k8s.io/v1` | NetworkPolicies: read and compare workload/peer Pod selectors against current source labels |
| `policy/v1` | PodDisruptionBudgets: read synchronized availability budget before controller retirement |
| `authorization.k8s.io/v1` | SelfSubjectAccessReview: ephemeral caller permission review; LocalSubjectAccessReview: optional namespace-scoped proof of service-account use of its required SCC when the SCC subject review selected another policy |
| `security.openshift.io/v1` | PodSecurityPolicySubjectReview: ephemeral review as the actual workload service account and groups, obtain SCC defaults; this is an OpenShift SCC API, not the removed upstream PodSecurityPolicy API |
| `quota.openshift.io/v1` | AppliedClusterResourceQuotas: read the namespace projection of shared quota usage; production does not create ClusterResourceQuotas |
| `image.openshift.io/v1` | ImageStreamTag API capability discovery when triggers require it. Production generates `image.openshift.io/triggers`; the OpenShift image controller performs updates. The converter does not fetch or mutate ImageStreamTag objects itself |

Live test drivers have additional dependencies, including Namespace labels/CRUD, Routes, ImageStreams/imports/tags, BuildConfigs/Builds, ConfigMaps/Secrets, service accounts and namespace RBAC, TokenRequests, and worker Leases. Authorized volume/quota/SCC cases add their own fixture operations. These harness operations must be assessed separately and must not be advertised as ordinary converter RBAC requirements. Completing that detailed harness inventory remains part of this review.

## Findings addressed in the first pass

**In-tree CephFS and RBD:** Kubernetes [1.31 release notes](https://github.com/kubernetes/kubernetes/blob/aea561709867d4039260bd462cb456b9b46906a1/CHANGELOG/CHANGELOG-1.31.md) remove both plugins and RBD CSI migration support. Their still-decodable volume fields do not establish mountability. The tool now classifies inline sources for manual migration, preserves them in paused review output, rejects old saved plans for new migration operations, checks admission-added inline volumes, and rejects PVC backing PVs using these plugins before source/HPA mutation. Replacement requires reviewed CSI storage and data migration. Other in-tree drivers with transparent CSI migration are not automatically included in this removal rule.

**`gitRepo`:** Kubernetes [1.33 release notes](https://github.com/kubernetes/kubernetes/blob/aea561709867d4039260bd462cb456b9b46906a1/CHANGELOG/CHANGELOG-1.33.md) disable this volume plugin by default, with a feature-gate opt-in. The same inline-volume guard requires a reviewed replacement, such as an init-container checkout into `emptyDir`. It conservatively applies across the supported range, including clusters where the plugin remains enabled. API dry run cannot prove kubelet support.

The storage guard was introduced in `52c5386`; `4ce61e7` preserves explicit recovery of old saved plans. Recovery bypasses this new portability classification only, retains ownership/spec checks, stops the destination first, and verifies restored source readiness/health. It cannot revive a removed driver. Regression tests cover preservation/immutability, old-plan stage/preflight/cutover refusal, admission-added volumes, CephFS/RBD PV backing, supported CSI/`emptyDir`, and recovery of a pre-guard plan. Actual live refusal tests deliberately use zero application replicas; they do not claim successful mounts of removed plugins.

## Findings still requiring assessment

The raw SCC and Pod spec guards (`278d942`) reject newer security/volume/resource fields before typed conversion. Required-SCC handling (`618831f`) adds the optional namespaced access review and verifies the effective policy; the API/fields are present in [Kubernetes v1.27.16 source](https://github.com/kubernetes/kubernetes/blob/v1.27.16/staging/src/k8s.io/api/authorization/v1/types.go). Actual [4.21 regression evidence](admission-scc-live-tests.md) demonstrates old preflight acceptance, current refusal with an unchanged healthy source, then successful migration/recovery after authorization restoration. This does not complete the whole per-release assessment.

- Kubernetes 1.33 deprecates core `v1` Endpoints while explicitly retaining support. The tool currently reads Endpoints; assess the impact and future EndpointSlice path without describing deprecation as API removal. Source: [1.33 changelog](https://github.com/kubernetes/kubernetes/blob/aea561709867d4039260bd462cb456b9b46906a1/CHANGELOG/CHANGELOG-1.33.md).
- OpenShift 4.20 enables Linux user namespaces and introduces `restricted-v3`/`nested-container` SCCs. Review SCC defaults and raw-to-typed Pod handling, including newly added fields that an older client could discard. Source: [4.20 new features](https://github.com/openshift/openshift-docs/blob/08bc31a85b65f871838334f1cfd911ef08e77f69/modules/rn-ocp-release-notes-new-features-and-enhancements.adoc).
- OpenShift 4.19 enables node-bound service-account tokens by default. Review authentication/revocation handling and distinguish this behavior from the actual signed-token expiry tests on 4.21. Source: [4.19 notes](https://github.com/openshift/openshift-docs/blob/eafb4a5cfd576aba7dd83307dd121274a52c4ab4/release_notes/ocp-4-19-release-notes.adoc).
- OpenShift 4.15 stops creating the deployer service account when the DC capability is disabled. Verify disabled-capability discovery/offline behavior and associated controller/RBAC changes across all minors. Source: [4.15 notes](https://github.com/openshift/openshift-docs/blob/ed0612e21c09eafcc9492f79195fa38254c3e1f9/release_notes/ocp-4-15-release-notes.adoc).
- Finish all per-release removals, field/default/feature-gate changes, dry-run/admission behavior, controller/image semantics, resource accounting/scheduling, storage migration, and applicable patch advisories against the inventory. Review aligned client-library candidates against these contracts; SDK compatibility is separate from `kubectl` version-skew policy.
- Add any required guards/tests and produce the final per-release/resource disposition. Only then update matrix/support/release policy to consume review-based assumptions for unavailable 4.14–4.20 targets. Preserve all actual artifact hashes and never synthesize integration passes.
- Audit raw Deployment/HPA admission responses before typed decoding. The new raw-spec guard covers SCC and Pod responses; it does not establish complete raw-field coverage for every resource boundary.
