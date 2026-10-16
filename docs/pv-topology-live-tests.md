# PV topology labels before source retirement

The consolidated release review found that the scheduler evaluates beta/GA PV zone and region labels independently of `spec.nodeAffinity`. The tool previously counted spare capacity in an incompatible zone. Commit `10f72e4` now requires manual scheduling review for these labels before changing a source or HPA. It does not remove labels or rewrite storage.

## Actual OpenShift result

On **OpenShift 4.21.36 / Kubernetes v1.34.9**, all four PV-label variants passed their expected refusal checks: beta zone, beta region, GA zone and GA region. For each, the preceding binary's **read-only preflight** incorrectly accepted the constraint; the new binary's preflight and cutover both refused. All eight refusals preserved the original source Pod UID/readiness/HTTP, source spec/UID, HPA inventory and staged destination spec/UID. No old binary performed cutover.

After removing each owned test label with UID/resource-version checks, the original PV labels were restored. Normal preflight, cutover, marker reads from the regular CSI volume, rollback and rollback rerun passed. The same source/PVC/PV identities and original application response were retained. This reuses the existing approved 1Gi CSI fixture in `dcmig-nf-c-20261016-regular-pvc`; it provisions no new storage, namespace or SCC grant.

A separate run in `dcmig-storage-baseline-20261016` completed all **13 automatic baseline scenario IDs** on the same binary. Its partial/nonzero result correctly identifies nine supplemental scenario records still absent for this exact artifact. The targeted storage-marker result is not relabelled as complete volume-exclusivity or cross-zone scheduler qualification.

The first label-test attempt encountered a TLS handshake timeout during an observation read. Its `finally` cleanup restored the label; logs remain intact. A separate completion run revalidated the source and storage identities, repeated all four variants and finished migration/recovery. The incomplete attempt is not counted as a complete pass.

## Provenance and limits

- Native binary SHA-256: `811c3c6a8bc9ca63dcfc65920403d9718f48145eedf2dcddc780f0e15004b932`, `darwin/arm64`; production revision `10f72e4`.
- Local validation: **330 race tests/subtests**, **77.4% coverage**, `go vet`, zero vulnerability findings and zero production security issues; five platform builds. Other platform builds establish compilation only.
- [Structured evidence](../compatibility/evidence/2026-10-16-pv-topology-4.21.36.json) contains source/storage identities, all four variants, baseline IDs, hashes, build flags, first-attempt failure and raw-evidence paths.
- [Consolidated source review](compatibility-review.md) records the per-version findings and explicit 4.14–4.20 compatibility assumptions separately from live results.

This proves actual API-label refusal and subsequent regular-CSI migration/recovery. It does not execute replacement Pods in another zone, simulate CSI attach limits, certify every driver or transfer earlier binaries' broader results to this artifact. Preflight remains a point-in-time assessment; full release qualification remains incomplete.

The credential-free archive and complete manifest checksums are recorded in the structured evidence. Actual credential values are excluded; its source rebuild matches the exact live-executed binary. Final archive/rebuild metadata is appended after sealing.
