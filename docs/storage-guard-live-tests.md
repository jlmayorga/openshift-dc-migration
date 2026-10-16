# Legacy storage guards: actual OpenShift evidence

The rebuilt storage-guard binary was exercised on **OpenShift 4.21.36 / Kubernetes v1.34.9**. Four targeted outcomes are verified, including the documented follow-up inspections below, and all 13 automatic baseline scenario IDs completed. **Full qualification remains partial.** The earlier binary's 48 targeted cases remain historical evidence for their original hash.

| Case | Observed result |
| --- | --- |
| Inline CephFS | Actual new-app-rendered zero-replica DC; generation/live planning report manual migration, preserve the admitted volume in paused output, and refuse staging without source changes or a destination |
| Inline RBD | Same refusal; API-defaulted pool/user/keyring fields are retained |
| Inline `gitRepo` | Same refusal, verified by a read-only recheck after the original live-plan attempt encountered a TLS handshake timeout |
| Existing regular CSI PVC | Actual source → Deployment → source migration; canary verified during destination operation and after recovery; original source spec/HTTP and PVC/PV UIDs preserved, with final post-rollback inspection completed after a TLS timeout |

The negative cases use three isolated namespaces, `dcmig-storage-current-20261016-{cephfs,rbd,gitrepo}`, with zero application replicas. OpenShift retains completed deployer Pods even at zero replicas; those Pods were verified not to mount the legacy volumes. No legacy-volume application Pod starts and no fixture storage address is contacted. These are API/classification cases, not successful legacy-driver mount tests. Admission-added legacy volumes, CephFS/RBD PV backing and older saved-plan refusal/recovery are covered by local request-contract tests, separately from live results.

The CSI test reuses the authorized 1Gi gp3 volume in `dcmig-nf-c-20261016-regular-pvc`. Its healthy source and owned paused-zero destination were checked before regeneration of that destination and fresh planning/staging/preflight. No new PVC, PV, StorageClass or SCC grant was created. Source UID `75e7fb13-89d0-4330-beec-c18cc2ee47a1`, PVC UID `bca86984-9c1c-4b98-8a73-85b698683560` and PV UID `356e7dcb-4a0e-4a8b-9a17-c82ecc5e5ad7` remain unchanged. This single-node test does not qualify cross-node storage or node failure.

The final baseline completed schema, dry run, SCC admission, exclusive/overlap cutover, Service routing, HPA retargeting, native image triggers, interruption, rollback, safe rerun, stale-plan refusal and constraint-preflight rejection. Its result is still `partial` and exit is nonzero because the supplemental variant records were not supplied; a quota refusal is not a failed-runtime-rollout result.

## Provenance and validation

- Native artifact SHA-256: `d40a3091902443bb3c298959368ea071080b6109bae940c1206ecc1010ae5457`, `darwin/arm64`.
- Production source: `4ce61e7063ec550b844e4c5766e61850aa8d4590`; initial storage guard `52c5386`, explicit old-plan recovery exemption `4ce61e7`.
- Reusable driver: `tools/qualify_storage.py`; server-default preservation correction `fb9e4df`. Exact invocation records and the final audit helper are retained.
- Local checks: **175 Go race cases including subtests**, **75.3%** statement coverage, `go vet`, **19 Python tests**, zero vulnerability findings, zero security issues/compiler errors, and five platform cross-builds. Cross-builds establish compilation only.

[Structured evidence](../compatibility/evidence/2026-10-16-storage-guard-4.21.36.json) records exact hashes, source/driver provenance, cases, baseline metadata, limitations and historical attempts. Raw command logs, plans, reports, snapshots, helpers and validation results are retained privately under `.scratchpad/release-notes-review-20261016` and packaged with a checksum manifest.

The complete archive is `openshift-4.21.36-storage-guard-complete-evidence.tar.gz`, SHA-256 `4ff09afb8cb3dd57aed80035e056726f2508febd016367a3de011566cc9a9b1a`: 716 files, manifest SHA-256 `af98460c0cb4de7678be9a206528daa8497f09fd49e00ffc3fbb8bcf4140e2fd`. Every entry was verified and actual credential values were checked in memory and excluded. The source snapshot includes the embedded baseline catalog and rebuilds byte-for-byte to the tested native binary. Archive/rebuild metadata were appended to the public record after packaging to avoid circular checksums. Nine ownership-verified superseded zero-replica namespaces were removed; final fixtures and the existing CSI volume remain available.

## Failed attempts and completed rechecks

The first refusal driver expected no Pod objects and failed on completed DC deployers. Its next version passed CephFS/`gitRepo` but failed the RBD preservation assertion because it compared admitted defaults with the original request. Both were harness errors; the corrected driver passed all three on the earlier candidate. That candidate's CSI/baseline results also remain historical because it preceded the recovery exemption.

On the final binary, CephFS/RBD passed directly. `gitRepo` generation correctly classified the volume, but live-plan discovery failed with a TLS handshake timeout. A later generation/plan/stage recheck on the unchanged owned fixture passed. The final CSI cutover and rollback both returned success; its harness then failed on a post-rollback `oc get dc` TLS timeout. API readiness later returned `ok`, and a separate read-only audit verified source spec/health, destination paused at zero, the same data marker and unchanged PVC/PV identities. Original failed case records remain failed; these are explicitly combined protocol-and-recheck observations, not relabelled successful first attempts.

The [older-version release-note review](compatibility-review.md) remains in progress. This session does not establish compatibility assumptions for 4.14–4.20, nor transfer historical supplemental results to this new binary. Remaining qualification must use the exact candidate artifact.
