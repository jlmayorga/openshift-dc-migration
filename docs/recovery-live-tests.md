# Runtime failure and connectivity-loss evidence

All **15 additional targeted cases passed** on **OpenShift 4.21.36 / Kubernetes v1.34.9**, using the same native binary as the [26-case preflight session](preflight-live-tests.md). This brings that exact binary to **41 targeted passing cases**, plus the earlier 13 automatic baseline scenario IDs. Full release qualification remains partial.

A [subsequent seven-case session](worker-expiry-live-tests.md) covers singleton heartbeats, real token expiration and additional recovery guards. The 15 cases below retain their original scope and provenance.

## Lost responses after committed writes

The recovery driver forwards requests through a loopback-only authenticated `oc proxy` and a credential-free fault proxy. Each case allows one specific real API write to commit, withholds its success response, and returns unavailable responses to subsequent converter requests. It then restores access, explicitly rolls back and repeats rollback.

| Fault point | Actual committed write | Observed cluster state before recovery |
| --- | --- | --- |
| 1 | DELETE source HPA | HPA absent; source still running; destination paused at zero. |
| 2 | UPDATE source DC | Source triggers empty; source still running. |
| 3 | UPDATE source DC | Source replicas zero; destination paused at zero. |
| 4 | UPDATE source DC | Source parked paused at zero. |
| 5 | UPDATE destination Deployment | Destination activated; source remains retired. |
| 6 | CREATE retargeted HPA | HPA targets the Deployment; source remains retired. |

All six CLI invocations returned failure and retained an incomplete journal. The actual request logs identify the single withheld response and exact number of committed cutover writes; staging, authorization/SCC reviews and server dry runs do not count. Post-run audits verify source UID/spec recovery, full HPA spec recovery, retained destination UID paused at zero, unchanged Service UID/spec, and six healthy application-specific HTTP response markers. Recreated HPAs have new UIDs as expected.

The first attempt's driver incorrectly counted a dry-run HPA DELETE carried in a `DeleteOptions` body and retained staging requests in the cutover evidence. Although its case assertions passed and sources recovered, that attempt is explicitly **invalid qualification evidence**. The corrected driver parses DELETE options, resets the request log at cutover, and asserts the expected method/resource at each fault point. All six corrected cases passed in fresh namespaces. The original attempt and audit are retained rather than counted as passing results.

## Runtime and integration cases

| Cases | Result and observation |
| --- | --- |
| Initial destination startup failure | Admission/capacity preflight passes. A deliberate fixture exits 42 only in the labelled destination. Cutover fails without reporting ready, retains `destination-updated` recovery state, and leaves the source retired until explicit rollback restores its original spec and HTTP response. Recovery rerun succeeds. |
| Failed image rollout after cutover | A valid image with an incompatible listening port fails the existing readiness probe. A cutover rerun does not report it ready. Restoring the tag and explicitly recovering restores source health. |
| Cross-namespace image trigger | An actual `oc new-app --as-deployment-config` workload updates its named destination container from another lab namespace; the source remains retired, and recovery succeeds. |
| Disabled image trigger | Importing another image does not update a paused destination trigger; source recovery succeeds. |
| Two independent image triggers | Updating each tag separately changes only the named container; both updates and recovery pass. The sidecar sleeps, avoiding assumptions about configurable HTTP ports; main-Service health is verified. |
| Restricted RBAC | DC-list-only generation succeeds without namespace reads; denied listing, Deployment creation and cutover updates return visible failures before unauthorized changes. |
| Quota admission | Dry run and staging refuse Deployment creation under quota; no destination persists and the source stays unchanged. |
| Foreign destination | Planning refuses an unrelated same-named Deployment and preserves its UID/spec. |
| Denied HPA deletion | Cutover refuses before source changes; original HPA UID and zero-replica destination remain unchanged. |

Runtime startup failure demonstrates why successful preflight remains a point-in-time assessment rather than a startup guarantee.

## Provenance and reproducibility

- Binary SHA-256: `f19a4c006e239b3c1a332f9aefc3c4ee5dc646dfba41e029a22634e98f0bb3f7`, `darwin/arm64`, Go source `12c0f15c5aa7e2531fc7b1fb573c0a2fb49d915a`. Go source and executable remain unchanged from the preceding session.
- Driver changes: `c8e2cc0` excludes review POSTs; `17211ce` adds initial startup failure and a portable sidecar fixture; `02fdca2` excludes DELETE body dry runs and verifies every fault point. [Structured evidence](../compatibility/evidence/2026-10-16-runtime-recovery-4.21.36.json) records exact invocation revisions, driver hashes, fixture/dependency hashes, cases and archive checksums.
- Reproduce with the [live drivers](qualification.md#additional-live-variant-drivers) and new-app cases `cross-image,disabled-image,failed-initial-rollout,failed-image-rollout`. The `multi-image` feature case verifies both named-container updates. All drivers require an explicit authorized target and mutation acknowledgement.
- Credential-free archive: `.scratchpad/supplemental-live-20261016/openshift-4.21.36-runtime-recovery-evidence.tar.gz`. It includes commands, raw snapshots, plans, journals, fault logs, source/drivers, HTTP audit and the invalid first attempt. Kubeconfigs, credentials and actual admin/test secret values are excluded; all internal checksums are verified.

Fourteen local Python tests, Black and diff checks pass. The preceding 148 Go race cases, vet, scanners and five cross-builds apply to unchanged Go source; other compiled artifacts were not executed live. All final fixtures are isolated labelled namespaces. Sources are recovered and destinations paused at zero. This session creates no node, PV, StorageClass or SCC grant. Short-lived namespace account tokens remain private.

This tests a lost API response through a local proxy, not every network partition or control-plane failure. Singleton-worker heartbeat/failure/cancellation tests, expired credential profiles, other admission/runtime combinations, the 4.14 floor and the remaining minor/platform matrix are still outstanding. Existing foreign-object/orphan-Pod safeguards require their own checks; successful recovery of these six normal write sequences does not prove every possible recovery state.
