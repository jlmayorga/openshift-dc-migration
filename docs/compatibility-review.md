# OpenShift compatibility assessment and prerequisites

The historical source-impact assessment is complete within the inventoried baseline scope. **OpenShift 4.14–4.20 are assumed compatible from official-source review, subject to the manual guards and prerequisites below.** No actual older-cluster integration pass is claimed. OpenShift 4.21 also has a source assessment and separately recorded actual 4.21.36 qualification for exact historical Linux amd64 and Darwin arm64 binaries.

The completed conclusions remain useful operator prerequisites. Their source-bound applicability belongs to the recorded historical source, adopted v0.37.1 clients, catalog and fixtures. **All current maintenance binaries require reassessment and new qualification.** Wider versions/platforms remain untested; see [coverage](compatibility.md) and [release policy](release-scope.md).

Durable per-release summaries remain under `compatibility/reviews/`. A trusted external bundle retains exact historical assessment snapshots and hash-pinned resolution overlays. Current summaries cannot replace those immutable verification inputs. The review covers relevant release notes, API migrations, controller/admission changes and patch conditions; it does not certify arbitrary extensions or every cluster advisory.

## Version mapping and source provenance

Each mapping below comes from the official release's “About this release” text. Immutable official OpenShift documentation sources and upstream Kubernetes changelogs provide reproducible references.

| OpenShift | Kubernetes | Official release-note source | Assessment |
| --- | --- | --- | --- |
| 4.14 | 1.27 | [4.14 source](https://github.com/openshift/openshift-docs/blob/9e1779093c4cacdc324992e84c0b03900a3a9ffe/release_notes/ocp-4-14-release-notes.adoc) | Assessed; [impact record](../compatibility/reviews/4.14-assessment.json), conditional review assumption |
| 4.15 | 1.28 | [4.15 source](https://github.com/openshift/openshift-docs/blob/ed0612e21c09eafcc9492f79195fa38254c3e1f9/release_notes/ocp-4-15-release-notes.adoc) | Assessed; [impact record](../compatibility/reviews/4.15-assessment.json), conditional review assumption |
| 4.16 | 1.29 | [4.16 source](https://github.com/openshift/openshift-docs/blob/d8ed17c431a59e24ff889c58eb03d8d4e5068d70/release_notes/ocp-4-16-release-notes.adoc) | Assessed; [impact record](../compatibility/reviews/4.16-assessment.json), conditional review assumption |
| 4.17 | 1.30 | [4.17 source](https://github.com/openshift/openshift-docs/blob/6c0902cc63abcac6b44a6467ca42be285ec01aad/release_notes/ocp-4-17-release-notes.adoc) | Assessed; [impact record](../compatibility/reviews/4.17-assessment.json), conditional review assumption |
| 4.18 | 1.31 | [4.18 source](https://github.com/openshift/openshift-docs/blob/e3d56cca51bcae6fd417bde29798512fa5f40723/release_notes/ocp-4-18-release-notes.adoc) | Assessed; [impact record](../compatibility/reviews/4.18-assessment.json), conditional review assumption |
| 4.19 | 1.32 | [4.19 source](https://github.com/openshift/openshift-docs/blob/eafb4a5cfd576aba7dd83307dd121274a52c4ab4/release_notes/ocp-4-19-release-notes.adoc) | Assessed; [impact record](../compatibility/reviews/4.19-assessment.json), conditional review assumption |
| 4.20 | 1.33 | [4.20 About this release](https://github.com/openshift/openshift-docs/blob/08bc31a85b65f871838334f1cfd911ef08e77f69/modules/rn-ocp-release-notes-about-this-release.adoc) | Assessed; [impact record](../compatibility/reviews/4.20-assessment.json), conditional review assumption |

The [4.21 / Kubernetes 1.34 assessment](../compatibility/reviews/4.21-assessment.json) includes immutable official source provenance. Actual 4.21.36 qualification remains tied to each recorded artifact, separately from this source review.

## Production API and operation inventory

The implementation under `internal/migration/` uses the following production operations. The baseline field catalog is generated from Kubernetes 1.27.16 OpenAPI. Network traffic uses JSON; optional APIs are discovered when needed.

| API | Resources and use |
| --- | --- |
| `apps.openshift.io/v1` | DeploymentConfigs: list/get, preserve source snapshot, dry-run and actual updates to triggers/replicas/pause/selector, explicit source restoration |
| `apps/v1` | Deployments: get, dry-run/actual create/update; ReplicaSets: list, representative create dry run, ownership/termination checks; StatefulSets/DaemonSets: controller overlap inventory |
| `batch/v1` | Jobs: controller overlap inventory; production does not create replacement hook Jobs |
| core `v1` | Pods: namespace and cluster inventory, raw create dry run, readiness/ownership/termination and allocation checks; RCs: source ownership/retirement; Services: inventory/read; Endpoints: traffic verification; ResourceQuotas/LimitRanges: read; PVCs/PVs: bound storage/topology/plugin checks; Nodes: placement/capacity inventory; target Namespace labels: read only when optional cluster policies exist |
| `autoscaling/v2` | HPAs: raw list/read/create-response baseline validation, dry-run/actual delete/create for temporary freeze and retarget/restoration; recovery deletion uses raw ownership metadata |
| `networking.k8s.io/v1` | NetworkPolicies: read and compare workload/peer Pod selectors against current source labels |
| Optional `policy.networking.k8s.io/v1alpha1` | Discover and list cluster AdminNetworkPolicies/BaselineAdminNetworkPolicies; compare subject/peer Pod selectors against actual source labels and target Namespace labels. Unknown policy APIs/fields require manual review; no policy writes |
| `policy/v1` | PodDisruptionBudgets: read synchronized availability budget before controller retirement |
| `authorization.k8s.io/v1` | SelfSubjectAccessReview: ephemeral caller permission review; LocalSubjectAccessReview: optional namespace-scoped proof of service-account use of its required SCC when the SCC subject review selected another policy |
| `security.openshift.io/v1` | PodSecurityPolicySubjectReview: ephemeral review as the actual workload service account and groups, obtain SCC defaults; this is an OpenShift SCC API, not the removed upstream PodSecurityPolicy API |
| `quota.openshift.io/v1` | AppliedClusterResourceQuotas: read the namespace projection of shared quota usage; production does not create ClusterResourceQuotas |
| `image.openshift.io/v1` | ImageStreamTag API capability discovery when triggers require it. Production generates `image.openshift.io/triggers`; the OpenShift image controller performs updates. The converter does not fetch or mutate ImageStreamTag objects itself |

Live test drivers have additional dependencies. The following operations are fixture setup, observation or fault injection, and must not be advertised as ordinary converter RBAC requirements. This inventory covers the `tools/qualify_*.py` drivers and their shared helpers; their source-based per-release dispositions are included in the impact records; actual fixture execution on older releases remains unavailable.

| Additional API/resources | Qualification use |
| --- | --- |
| core `v1` Namespaces, ConfigMaps, Secrets, ServiceAccounts | Labelled namespace creation; application configuration; scoped test identities; namespace-local credential material retained privately |
| core `v1` Pods, `pods/exec`, `pods/log`, Events | Runtime/ownership observations; PVC marker writes/reads; application/worker logs; failure evidence; explicit orphan-Pod fixture creation/deletion |
| core `v1` `serviceaccounts/token` (`authentication.k8s.io/v1` TokenRequest body) | Server-issued short-lived tokens for restricted-access and actual-expiry tests; not production credential minting |
| `rbac.authorization.k8s.io/v1` Roles/RoleBindings | Namespace-scoped reader, worker Lease, required-SCC and deliberately denied permissions; references to existing SCC ClusterRoles where the fixture requires them |
| `coordination.k8s.io/v1` Leases | Singleton fixture's Pod-UID owner heartbeat, resource-version compare-and-swap and sampled recovery evidence |
| `route.openshift.io/v1` Routes | Expose test Services and independently verify HTTP responses during cutover/recovery |
| `image.openshift.io/v1` ImageStreams, ImageStreamTags/imports | Immutable image imports, same/cross-namespace trigger updates and restored tags |
| `build.openshift.io/v1` BuildConfigs/Builds and binary-build subresources; `template.openshift.io/v1` Templates | Real `oc new-app`, processed templates, Docker/S2I binary builds and rebuild-trigger observations |
| `config.openshift.io/v1` ClusterVersions and `/version` | Observe exact release image, OpenShift patch, Kubernetes version and capabilities for provenance |
| `quota.openshift.io/v1` ClusterResourceQuotas; core `v1` ResourceQuotas/LimitRanges | Isolated namespace/shared-quota and admission refusal fixtures; synchronized usage checks |
| core `v1` PVs/PVCs; `security.openshift.io/v1` SCCs | Separately authorized regular CSI volume, or optional static hostPath fixture; existing-policy inspection and namespace-only SCC-use grants. No SCC modification or StorageClass/node creation |

Deployment/ReplicaSet/RC/HPA/Service/Endpoint, NetworkPolicy, PDB, SCC review and access-review operations overlap the production inventory. CLI commands also use discovery and controller-generated resources; successful 4.21 fixture execution does not establish that every CLI/controller behavior is unchanged on older releases.

## API-version removals and Endpoints

The [upstream migration guide](https://kubernetes.io/docs/reference/using-api/deprecation-guide/) identifies the following removed versions within the reviewed Kubernetes range. Comparing those versions with the inventories above finds no direct call to a removed API. This conclusion concerns API serving only; field/default/controller/storage changes are assessed separately in the per-release impact records.

| OpenShift / Kubernetes | Removed API version listed by the guide | Tool impact |
| --- | --- | --- |
| 4.14 / 1.27 | `storage.k8s.io/v1beta1` CSIStorageCapacity | Neither production nor the qualification fixtures call this API |
| 4.15 / 1.28 | No removal listed for this minor | No removal identified by this guide |
| 4.16 / 1.29 | `flowcontrol.apiserver.k8s.io/v1beta2` FlowSchema/PriorityLevelConfiguration | Neither inventory configures API Priority and Fairness |
| 4.17 / 1.30 and 4.18 / 1.31 | No removal listed for these minors | Storage-plugin removals are a separate runtime finding below |
| 4.19 / 1.32 | `flowcontrol.apiserver.k8s.io/v1beta3` FlowSchema/PriorityLevelConfiguration | Neither inventory calls this version |
| 4.20 / 1.33 | No removal listed for this minor | Endpoints deprecation is addressed separately below |

Kubernetes [1.33 release notes](https://github.com/kubernetes/kubernetes/blob/aea561709867d4039260bd462cb456b9b46906a1/CHANGELOG/CHANGELOG-1.33.md) explicitly retain support for core `v1` Endpoints while deprecating it. The tool reads this API to match Service endpoint Pod UIDs during destination/source health verification. A deprecation warning does not change that request contract, and actual 4.21 runs exercise it. No API-removal mitigation is required for 4.20. Moving to EndpointSlice remains a maintenance follow-up; this finding does not certify all networking behavior, large endpoint sets or every release's controllers.

## Findings addressed in the first pass

**In-tree CephFS and RBD:** Kubernetes [1.31 release notes](https://github.com/kubernetes/kubernetes/blob/aea561709867d4039260bd462cb456b9b46906a1/CHANGELOG/CHANGELOG-1.31.md) remove both plugins and RBD CSI migration support. Their still-decodable volume fields do not establish mountability. The tool classifies inline sources for manual migration, preserves them in paused review output, rejects old saved plans for new migration operations, checks admission-added inline volumes, and rejects PVC backing PVs using these plugins before source/HPA mutation. Replacement requires reviewed CSI storage and data migration. Other in-tree drivers with transparent CSI migration are not automatically included in this removal rule.

**`gitRepo`:** Kubernetes [1.33 release notes](https://github.com/kubernetes/kubernetes/blob/aea561709867d4039260bd462cb456b9b46906a1/CHANGELOG/CHANGELOG-1.33.md) disable this volume plugin by default, with a feature-gate opt-in. The same inline-volume guard requires a reviewed replacement, such as an init-container checkout into `emptyDir`. It conservatively applies across the supported range, including clusters where the plugin remains enabled. API dry run cannot prove kubelet support.

Explicit recovery of older saved plans bypasses storage-portability classification only. It retains ownership/spec checks, stops the destination first and verifies restored source readiness/health. It cannot revive a removed driver. Zero-replica legacy-plugin refusal tests do not establish successful mounts of those plugins.

**Cluster admin network policies:** The [4.14 notes](https://github.com/openshift/openshift-docs/blob/9e1779093c4cacdc324992e84c0b03900a3a9ffe/release_notes/ocp-4-14-release-notes.adoc#L875) introduce optional cluster policy capabilities. Namespace NetworkPolicy checks alone miss admin-policy subject and peer selectors that distinguish DC-added labels or the destination migration label. The converter discovers the optional API, inventories policies and compares those selectors before source/HPA mutation; denied or unassessed capabilities require manual review. Empty/absent APIs and equivalent selection remain usable. Recovery bypasses this gate. This is a selector-preservation check, not a complete priority/traffic simulation; see [OpenShift policy semantics](https://docs.redhat.com/en/documentation/openshift_container_platform/4.20/html/network_security/admin-network-policy).

The Deployment controller adds [`pod-template-hash`](https://kubernetes.io/docs/concepts/workloads/controllers/deployment/#pod-template-hash-label) after Pod dry run. The converter therefore requires manual review for namespace/admin Pod selectors that depend on this future label; comparing two temporarily absent values would miss the change. This guard does not predict hashes across server versions.

## Authentication and disabled capabilities

OpenShift [4.19 adds node-bound service-account tokens](https://github.com/openshift/openshift-docs/blob/eafb4a5cfd576aba7dd83307dd121274a52c4ab4/release_notes/ocp-4-19-release-notes.adoc#L68), and upstream 1.29–1.33 notes describe additional JWT metadata and binding validation. The production client loads standard kubeconfig credentials and treats bearer tokens as opaque; it neither mints tokens nor parses their claims. New claim shapes and the optional node binding therefore require no request-format change in the converter. Workload token-projection settings and service-account names are preserved. Revocation or expiration can still happen during a run: authorization/transport failures remain failures, with incomplete journals and explicit recovery. The actual [signed-token expiry result](compatibility.md) is historical evidence for that recorded binary, not a node-deletion or older-server test.

OpenShift [4.15 changes generated secrets when ImageRegistry is disabled](https://github.com/openshift/openshift-docs/blob/ed0612e21c09eafcc9492f79195fa38254c3e1f9/release_notes/ocp-4-15-release-notes.adoc#L1140), and [4.16 stops generating legacy API-token secrets and rotates bound image-pull tokens](https://github.com/openshift/openshift-docs/blob/d8ed17c431a59e24ff889c58eb03d8d4e5068d70/release_notes/ocp-4-16-release-notes.adoc#L1404). The converter does not retrieve automatically named service-account secrets or depend on the first Secret in an account. Existing valid kubeconfig credentials and functioning image-pull credentials remain operator prerequisites. Legacy credentials invalidated by token cleanup must be replaced; the converter does not re-enable them. The qualification harness explicitly requests short-lived tokens rather than depending on automatic Secret creation.

The [4.15 deployer-account change](https://github.com/openshift/openshift-docs/blob/ed0612e21c09eafcc9492f79195fa38254c3e1f9/release_notes/ocp-4-15-release-notes.adoc#L1167) applies when DeploymentConfig capability is disabled. The converter does not create that account or enable cluster capabilities. It discovers the DC group/resource and distinguishes unavailable APIs from denied discovery, authentication failure and an empty namespace. Unavailable live DC APIs produce an exported-input diagnostic; offline conversion remains usable and its incomplete-inventory plan cannot execute. Contract tests exercise the unavailable, denied, expired, empty and offline paths. Actual operation errors are still checked after access reviews. Build/registry/cross-namespace image setup remains a separate harness or workload prerequisite.

These dispositions address the named token/capability changes; referenced authentication guidance, node-audience/storage interactions, patch advisories and the maintained-client decision remain part of the wider review.

## Kubernetes 1.28 field and default changes

The [4.15 notes remove NetworkPolicy status](https://github.com/openshift/openshift-docs/blob/ed0612e21c09eafcc9492f79195fa38254c3e1f9/release_notes/ocp-4-15-release-notes.adoc#L1519); the converter only reads its spec and never infers CNI readiness from status. No status request or write needs replacement. Upstream [1.28 changes](https://github.com/kubernetes/kubernetes/blob/aea561709867d4039260bd462cb456b9b46906a1/CHANGELOG/CHANGELOG-1.28.md) also move host-network `hostPort` defaulting to actual Pod creation. Automatic host-namespace execution already requires manual review, and preflight validates the admitted Pod independently of the Deployment template. Namespace node-selector admission and explicit `nodeName` are checked together against actual node eligibility.

Native sidecars add a post-floor init-container `restartPolicy`; the raw baseline guard requires manual migration instead of dropping that field. For other running cluster Pods, allocation accounting includes restartable init sidecars. The PVC resize-status change does not affect the converter's Bound-phase/reference/topology checks; the tool does not resize or delete production claims. Driver-dependent SELinux mount optimizations and RWOP support retain their [4.15 storage prerequisites](https://github.com/openshift/openshift-docs/blob/ed0612e21c09eafcc9492f79195fa38254c3e1f9/release_notes/ocp-4-15-release-notes.adoc#L815), and successful mounting/relabeling remains a runtime check.

The webhook-authorizer access-review version default changes when unset, as confirmed by the [upstream implementation change](https://github.com/kubernetes/kubernetes/pull/116937). The converter explicitly reviews the version used by its actual endpoint (`v1`, HPA `v2`, or optional admin-policy `v1alpha1`). A preliminary SSAR still cannot grant a later operation: actual reads/dry runs enforce the exact endpoint. Required-SCC LocalSubjectAccessReview already sets `v1`. The final impact assessments additionally correlate PriorityClass quota, CSI/kernel and authorization changes; full runtime/client qualification remains a separate requirement.

## Consolidated resource coverage

| Resource/operation | Reviewed contract and disposition |
| --- | --- |
| DC/RC and Deployment/ReplicaSet | Stable APIs; DC deprecation/capability discovery; exact source and destination admission; controller ownership and readiness; unknown raw spec fields require manual review |
| Pods, quotas, LimitRanges and placement | Baseline fields, native-sidecar/resize/Pod-budget accounting, PriorityClass patch, admitted defaults and cumulative headroom; unassessed placement semantics fail closed |
| HPA | autoscaling/v2 full-spec preservation/retarget/recovery; post-baseline fields require manual review; metrics availability remains a controller prerequisite |
| SCC and access reviews | Actual workload SA/defaults and required-SCC rights; explicit endpoint versions; namespace UID/user-namespace/SELinux policy evaluated by current admission |
| Services/Endpoints and network policies | Endpoints retained despite deprecation; source/destination selector preservation including optional admin policies; external/protocol traffic needs application checks |
| PDB | Stable policy/v1 and synchronized availability checked before direct controller retirement; no reliance on eviction enforcement alone |
| PVC/PV/storage | Bound references and node affinity; removed plugins and independent PV topology labels guarded; CSI translation distinguished from removed plugins; runtime driver/mount limits remain prerequisites |
| ImageStreams, builds and registry | Preserve images and trigger format; unresolved images refuse migration; fixture builds/imports require enabled capabilities, credentials, trust and functioning controllers |
| Harness-only APIs | Stable Route, TokenRequest, RBAC, Lease, template/build and isolated quota/storage operations reviewed separately from production RBAC |

PriorityClass quota fix PR117825 was backported in Kubernetes 1.27.2, before the official OpenShift 4.14.0 payload's Kubernetes 1.27.6. No extra floor guard is needed. The 4.16 eviction note OCPBUGS-61582 maps to HyperShift's IBM Cloud node-monitor grace-period fix, rather than a converter YAML-format change; the 4.16 assessment retains the primary correlation. AzureFile/Portworx CSI translation is not treated as the nonfunctional CephFS/RBD removal.

## Conditional patch and runtime prerequisites

These are documented fix releases, not proven earliest affected versions. For an affected workload, use a payload carrying the applicable vendor fix and validate its actual traffic/runtime behavior, or use a reviewed manual migration. Upstream patch numbers alone do not prove OpenShift backport status.

| Workload condition | Documented fixes | Source assessment |
| --- | --- | --- |
| `shareProcessNamespace: true` with affected runc | 4.17.45 / 4.18.30 | [4.17](../compatibility/reviews/4.17-assessment.json), [4.18](../compatibility/reviews/4.18-assessment.json) |
| UDP Service port differs from targetPort | 4.17.49 / 4.18.32 | Same records, with immutable z-stream links and issue IDs |
| Remote UDP NodePort/LoadBalancer stale backends | 4.17.51 | [4.17](../compatibility/reviews/4.17-assessment.json) |
| systemd entrypoint ConfigMap mount permissions | 4.17.52 / 4.18.34 | [4.17](../compatibility/reviews/4.17-assessment.json), [4.18](../compatibility/reviews/4.18-assessment.json) |
| External-gateway stale routes after Pod deletion while node inactive | 4.18.38 | [4.18](../compatibility/reviews/4.18-assessment.json) |

SCC readmission behavior on older payloads, SHA-1 Route certificate rejection, registry/build credential rotation, CSI driver/attachment limits, vSphere/vSAN platform fixes, ID-mapped filesystem support and image trust/signatures remain named prerequisites in the individual records. OpenShift 4.20 enabling user namespaces does not move every Pod into one: `hostUsers` defaults true; opting into false requires suitable SCCs, ID ranges and storage. Namespace SELinux defaults can inject newer fields and correctly require manual migration. The tool does not change SCCs, image policies or storage labels to bypass these requirements.

HTTP health and Endpoint UID convergence do not prove UDP, secondary-network, external-gateway or every CNI path. API admission cannot pull an image, check node-local AppArmor profiles, reserve a CSI attachment slot or prove a successful mount. The assumption applies with these explicit prerequisites and existing manual guards; it is not a claim that every historical patch or arbitrary extension is safe.

## Client decision and release policy

The historical assumption binds aligned **v0.37.1** libraries, shipped Go source, the embedded 1.27 field catalog and fixtures. The client delta assessment covers taint matching, baseline toleration operators and JSON negotiation despite ambient CBOR flags. Per-client adapters force JSON reads/writes and reject CBOR responses without changing global flags. Discovery, stable resource endpoints, raw unknown-field guards and actual operation errors remain authoritative.

The release verifier accepts assumptions only for explicitly configured 4.14–4.20 rows with matching recorded source/dependency/fixture identities and verified historical assessment snapshots. These rows remain separate from actual integrations. Changed source, catalog, fixtures or dependencies require reassessment. Historical 4.21 actual qualification applies only to the exact scoped binaries. OpenShift 4.22, other native platforms and the broader full-matrix gate remain incomplete. No old passing record qualifies current maintenance output.
