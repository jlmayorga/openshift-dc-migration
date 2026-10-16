# Singleton workers, expired credentials and recovery guards

All **seven targeted cases passed** on **OpenShift 4.21.36 / Kubernetes v1.34.9**, using the unchanged native binary from the [preflight](preflight-live-tests.md) and [runtime/recovery](recovery-live-tests.md) sessions. That binary now has **48 targeted passing cases**, plus 13 automatic baseline scenario IDs. Full release qualification remains partial.

| Case | Actual observation |
| --- | --- |
| Singleton success | An active-owner Lease heartbeat moves from source Pod UID to destination Pod UID and back to a new source Pod UID. Exclusive cutover, application responses, full source-spec recovery and recovery rerun succeed. |
| Singleton startup failure | Admission/capacity checks pass; destination startup deliberately exits 42. Failed/incomplete migration state is retained. Explicit recovery restores the source worker, heartbeat and application response. |
| Singleton cancellation | SIGTERM interrupts the CLI after the destination worker acquires the Lease while activation is incomplete. The CLI fails, retains recovery state, and explicit rollback/rerun restores the source worker. |
| Expired credentials | The same real signed token succeeds before expiry, later receives HTTP 401 and produces a failed CLI report with nonzero exit. Source UID/spec/HTTP stay unchanged and no Deployment is created. |
| Orphan destination | After actual destination startup failure and source retirement, the owned destination is removed and an idle ownerless Pod is introduced with the migration ID. Rollback refuses to restore the source while that Pod exists. Removing only that test Pod permits recovery and a recovery rerun. |
| Changed staged destination | A staged spec change is detected before cutover; the source stays unchanged and explicit recovery succeeds. |
| Foreign controller overlap | Planning rejects an unrelated controller whose selector overlaps the workload and preserves its UID/spec. |

## Singleton evidence

The fixture is a running worker with a per-second Lease heartbeat and an HTTP response containing its Pod UID. Its service account can only get/update one named Lease in its own namespace. Lease updates use resource-version compare-and-swap; a different active holder causes the fixture to fail. Shutdown releases ownership. No storage, SCC grant or cluster role is created.

Across **248 Pod/Lease samples**, no source/destination Pod overlap and no read errors were observed: 96 samples for success, 92 for startup failure and 60 for cancellation. Distinct initial/destination/recovered worker UIDs and advancing heartbeat timestamps are retained. The startup-failure destination never acquires the Lease. Post-run checks independently verify all three recovered DC UIDs/specs, destination UIDs paused at zero, active source Lease holders and matching application responses.

Sampling cannot observe every instant, and the fixture's Lease protects its work independently. The evidence combines those ownership checks with the migration's sampled absence of overlapping Pods; it does not claim that a singleton application can safely select overlap mode.

## Expiration and failed attempts

The first worker setup failed before migration: the API rejected Lease timestamps without the six fractional digits required by `MicroTime`. The corrected fixture uses that format and fails visibly on unexpected Lease API errors. Two ownership-verified failed setup namespaces were removed after stopping the driver; logs and API responses remain evidence. All three final worker cases passed in fresh namespaces.

The first expiry assertion expected rejection five seconds after the signed JWT expiry. The server still accepted the token, so that assertion failed and is not counted as passing. A later direct API probe returned **401** with a measured server-clock offset of about **0.05 seconds**. The corrected helper then used the **same unchanged token**, source and original pre-expiry success record to verify actual CLI authentication failure, nonzero exit and unchanged source health. No token was forged/replaced, account revoked or clock changed. The exact rejection boundary and server-side cause of the earlier acceptance are not inferred.

The public permission runner now waits for observed server rejection within a bounded deadline and records any post-timestamp acceptance. It also checks CLI exit status against the canonical report, so a failed-looking report with success exit cannot qualify the case. Reproduce with [the optional expiry case](qualification.md#additional-live-variant-drivers); the normal permission suite excludes its ten-minute wait.

## Provenance and limits

- Native binary SHA-256: `f19a4c006e239b3c1a332f9aefc3c4ee5dc646dfba41e029a22634e98f0bb3f7`, `darwin/arm64`, Go source `12c0f15c5aa7e2531fc7b1fb573c0a2fb49d915a`. No Go source or executable changed for this follow-up.
- [Structured evidence](../compatibility/evidence/2026-10-16-worker-expiry-4.21.36.json) records cases, UIDs, heartbeat counts, signed-token timing, actual rejection, driver revisions/hashes, prior-attempt failures and archive checksums. The expiry recheck explicitly records its reuse of the original valid-before-expiry fixture.
- Reproduce worker cases with [qualify_worker.py](../tools/qualify_worker.py), and `orphan-recovery,changed-target,foreign-controller-overlap` with [the new-app runner](qualification.md#apps-created-with-oc-new-app). Supply an explicit authorized disposable target, approved immutable image and mutation acknowledgement.
- Credential-free archive: `.scratchpad/worker-expiry-live-20261016/openshift-4.21.36-worker-expiry-evidence.tar.gz`. Commands, snapshots, plans/journals, worker logs/timelines, source/drivers and failed attempts are retained. Actual credentials, kubeconfigs and binaries are excluded; internal checksums and secret scanning are verified.

Eighteen local Python tests, Black and diff checks pass. The preceding 148 Go race cases, vet, scanners and five cross-builds apply to unchanged Go source; other platform artifacts were not executed live. Final dedicated namespaces remain available for inspection, with source workloads recovered. The orphan case intentionally has no destination Deployment after recovery.

This covers the named cases, not every external reconciler, scheduler, admission policy, storage failure or network partition. The user-requested [OpenShift 4.14–4.20 release-note review](backlog.md) remains planned; those compatibility assumptions have not yet been established. Other versions/platform artifacts remain unqualified.
