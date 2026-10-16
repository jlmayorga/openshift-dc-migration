# Conversion behavior

The baseline is OpenShift 4.14 (Kubernetes 1.27). The embedded field/required-property catalog is derived from the official Kubernetes v1.27.16 OpenAPI document; its URL and original SHA-256 are recorded in `compatibility/baseline-1.27.json`. Regenerate with `tools/extract-baseline.py`. This checks baseline field availability and required properties; typed decoding and local checks supplement it. It does not replace server validation, admission or OpenShift controller tests.

| DC feature | Output and operator action |
| --- | --- |
| Pod labels | Retained independently of metadata flags, including Service-facing `deploymentconfig`; add a stable migration selector label |
| Replicas | Explicit zero retained; omitted/null DC replicas default to zero, including offline conversion |
| Paused, minReadySeconds, revisionHistoryLimit | Retained; omitted readiness/history use Deployment defaults (0 and 10) |
| Rolling | RollingUpdate; preserve integer/percentage limits; missing limits default to 25% |
| Recreate | Recreate |
| Custom/unknown, test DC, hooks | Paused review manifest, actionable manual classification, nonzero exit, no automatic application |
| Automatic ImageChange | `image.openshift.io/triggers` per container, preserving source namespace and paused/disabled state |
| Unresolved automatic ImageChange | Read-only review allowed; live plan/staging/cutover blocked until initial build/import resolves the DC image |
| Unsupported/ambiguous ImageChange | Manual classification, no automatic application |
| ConfigChange | Deployment template changes cause rollouts |
| Omitted/null triggers | DC API ConfigChange default; warning recorded |
| Empty/no ConfigChange triggers | Paused review manifest; manual rollout procedure required |
| DC rollback | Deployment provides no equivalent automatic rollback guarantee; explicit recovery required |
| Deployer timing/deadlines | Not equivalent; warning recorded; nonempty custom deployer resources/metadata requires manual work |
| Unknown DC semantics/newer pod fields | Manual classification or validation error; never silently dropped |
| Inline `cephfs`, `rbd`, `gitRepo` volumes | Preserved in a paused review manifest; automatic migration blocked because their runtime plugins were removed/disabled within the target range |
| PVC backed by an in-tree CephFS/RBD PV | Pre-cutover refusal before source/HPA changes; replace with reviewed CSI storage first |
| Required SCC annotation | Preserve the pin; prove service-account access to that exact SCC and verify Pod admission used it. A namespaced access review is needed when the subject review selected another SCC |

Migration identifiers derive from source namespace, name and UID. Output contains no source owner references, resource version or server status. Conversion never modifies the source object. Hook Jobs, custom deployers and dependent-resource transitions require operator design; the tool does not invent substitutes.

The Deployment progress deadline defaults to 600 seconds and is raised above preserved `minReadySeconds` when necessary, so long readiness settings do not produce an invalid Deployment. Configure the operation/readiness timeout accordingly. Deployment templates reject active deadlines and ephemeral containers. HPA specs are checked as raw JSON against the 1.27 baseline before typed decoding; newer fields that an older client could drop require manual freeze/retargeting. Owner-managed/finalizer-bearing HPAs and active DC deployers are also blocked from automatic migration.

A source `pod-template-hash` label requires manual selector review because Deployment controllers replace it on ReplicaSets/pods. Pod-template owner references and finalizers require manual lifecycle review. Server-managed pod-template identity metadata is not copied to the destination; Service-facing labels and annotations remain independent.

SCC review and Pod admission can introduce fields absent from the original template. Preflight checks the entire raw returned Pod spec against the 1.27 field catalog before typed decoding, quota or placement assessment. Admission-added image volumes, supplemental-group policy, SELinux change policy and other post-baseline fields require manual review rather than being silently discarded or treated as fully assessed. Known baseline defaults, including `hostUsers: false`, remain usable. The same checks run before cutover; a refusal leaves the source, HPA and staged destination unchanged. This field check does not claim to simulate all runtime behavior of baseline fields.

Storage API fields can outlive their kubelet plugins. Kubernetes [1.31 removed the in-tree CephFS and RBD plugins](https://github.com/kubernetes/kubernetes/blob/master/CHANGELOG/CHANGELOG-1.31.md), including RBD CSI migration support; [1.33 disabled `gitRepo` by default](https://github.com/kubernetes/kubernetes/blob/master/CHANGELOG/CHANGELOG-1.33.md). The tool conservatively requires manual replacement across its supported range, including on an older server or one with `gitRepo` explicitly enabled. It does not move data, enable feature gates or create a replacement storage driver. A reviewed CSI PVC or an init-container checkout into `emptyDir` can replace these respectively. Conversion preserves the original volume fields for review. Stored plans are revalidated, and cutover checks admission-added volumes and PVC backing PVs; API dry-run acceptance alone cannot authorize source retirement. Other CSI attach/mount/runtime limits still require operator review and live qualification.

Explicit rollback of an older saved plan bypasses only this new storage-portability classification so that it can stop the destination and attempt restoration of the original source. Ownership/spec checks and restored readiness/health remain mandatory. This cannot make a removed plugin work; if the original source cannot start, recovery fails visibly instead of claiming success. Ordinary conversion, staging, preflight and cutover retain the guard.
