# Live OpenShift migration evidence — 2026-10-15

Actual target: **OpenShift 4.21.36 / Kubernetes v1.34.9**. The user authorized resource CRUD on this throwaway admin-connected cluster. Tests used dedicated labelled `dcmig-*` namespaces; no cluster, nodes or cloud storage were provisioned. Ordinary pod admission used `restricted-v2` with namespace-assigned UID/FSGroup/SELinux defaults.

**28/29 targeted cases passed**, plus all **13 automatic baseline scenarios**. Expected refusals count as passed only when the CLI returned failure and the assertions verified that unsafe changes did not occur. A failed setup attempt never counts as a migration pass. The full release qualification remains incomplete.

## Tested binary and evidence

- Native binary: `darwin/arm64`, Go 1.27.2, Go source revision `ccdd9fff809214b80ea7e1ea6866d5da07be342b`.
- SHA-256: `774404f71bc4434afcb40216cc0aa74bbf5bbc6903879818682c6851a5bd0505`.
- Aligned Kubernetes libraries: `v0.31.3`; 0.37.1 has not been adopted.
- [Structured results](../compatibility/evidence/2026-10-15-4.21.36.json) include exact release image, dependency/fixture hashes, source/destination UIDs and per-case observations.
- Raw reports, original DC snapshots, plans/journals, command stdout/stderr, admitted specs, Pod/RC/RS/Service/Endpoints/HPA/Route observations and failure attempts are retained in `.scratchpad/live-20261015/`.
- The credential-free evidence archive and its `SHA256SUMS` manifest are recorded in the structured results. Kubeconfigs, test-token profiles and actual credential values are excluded. The tested binary itself remains separate as `converter-final`.

## Final-binary cases

| Case | Outcome | Dedicated namespace / observation |
| --- | --- | --- |
| rolling-integer | passed | `dcmig-final-config-20261015-rolling-integer` |
| rolling-percent | passed | `dcmig-final-config-20261015-rolling-percent` |
| recreate | passed | `dcmig-final-config-20261015-recreate` |
| zero | passed | `dcmig-final-config-20261015-zero` |
| env-volume | passed | `dcmig-final-config-20261015-env-volume` |
| multi-container | passed | `dcmig-final-config-20261015-multi-container` |
| api-defaults | passed | `dcmig-final-config-20261015-api-defaults` |
| paused | passed | `dcmig-final-config-20261015-paused` |
| manual-triggers | passed | `dcmig-final-config-20261015-manual-triggers` |
| test-dc | passed | `dcmig-final-config-20261015-test-dc` |
| custom | passed | `dcmig-final-config-20261015-custom` |
| rolling-hook | passed | `dcmig-final-config-20261015-rolling-hook` |
| recreate-hook | passed | `dcmig-final-config-20261015-recreate-hook` |
| write-1 | passed | `dcmig-final-recover-20261015-write-1` |
| write-2 | passed | `dcmig-final-recover-20261015-write-2` |
| write-3 | passed | `dcmig-final-recover-20261015-write-3` |
| write-4 | passed | `dcmig-final-recover-20261015-write-4` |
| write-5 | passed | `dcmig-final-recover-20261015-write-5` |
| write-6 | passed | `dcmig-final-recover-20261015-write-6` |
| cross-namespace-image | passed | `dcmig-final-feature-20261015-cross-namespace-image` |
| multi-image | passed | `dcmig-final-feature-20261015-multi-image` |
| disabled-image | passed | `dcmig-disabled-retry-20261015-disabled-image` |
| restricted-rbac | passed | `dcmig-final-permission-20261015-rbac` |
| quota-admission | passed | `dcmig-final-permission-20261015-quota-admission` |
| foreign-destination | passed | `dcmig-final-permission-20261015-foreign` |
| singleton-worker | passed | `dcmig-final-singleton-20261015-worker` |
| expired-credentials | passed | `Authentication-only check` |
| denied-hpa-mutation | passed | `dcmig-final-hpa-denied-20261015-rbac-hpa` |
| persistent-volume | failed | `dcmig-final-volume-20261015-persistent-volume` |

Supported configuration cases execute source readiness → live plan → nonpersistent server dry run → paused zero-replica stage → stage rerun → cutover → completed rerun → rollback → recovery rerun. They compare source/desired fields and verify journal phases, final replica counts and restored source specs. The omitted-replica case additionally compares offline conversion with the actual API default of zero.

The six response-loss cases forward real writes to the API and withhold their successful replies: HPA deletion, source-trigger quiescence, source scale-to-zero, source parking, destination activation and HPA recreation. Every case verifies a failed/uncertain report, restores access, explicitly rolls back, and reruns recovery.

Image cases update real ImageStreamTags and wait for actual Deployment rollouts. The multi-container case changes each container independently; the disabled case proves a tag update does not change its image. Cross-namespace references use a separate test image namespace. All recover to the original source.

Permission tests use short-lived namespace-only service accounts: generation without namespace-read permission; denied DC listing, destination creation, source update and HPA deletion; and a genuine expired-token authentication failure. Quota tests exercise both create-time dry-run rejection and controller-time destination failure. Foreign Deployments retain their UID and spec.

Singleton-worker evidence contains source/destination heartbeat logs and a sampled Pod ownership timeline. It observes no concurrent source/destination worker; sampling supplements the tool journal and does not claim observation of every instant.

## Failures found and corrected

- `d3481d5`: actual recovery encountered a transient Route HTTP 503 after Pod/Endpoint readiness. Health now retries within the readiness timeout and preserves the final error if it never converges. Live recovery and persistent-failure unit tests pass.
- `ccdd9ff`: creating a DC without replicas produced zero on this server; offline conversion had chosen one. A failing regression reproduced the discrepancy before the fix. Offline and actual-server behavior now agree and the zero-replica migration passes.
- Harness corrections handle empty `oc get --ignore-not-found` output, wait for final source-controller status updates, normalize HTTP ports, and initialize an image-trigger rollout before disabling updates. Initial failures, one API TLS handshake timeout, and retries remain in the raw evidence.
- Static-volume attempts initially failed the marker precondition under SELinux. Source setup and controller admission must succeed before persistence can count as tested; those failures are retained separately from the migration cases.

## Local validation and remaining qualification

113 Go test cases including subtests pass with race detection; statement coverage is 77.6%. Vet, six Python gate tests, formatting, actionlint and all five cross-builds pass. Refreshed govulncheck has no symbol-level findings; gosec reports no issues or compiler errors.

This is actual 4.21.36 evidence for the exact native binary above. It does not qualify 4.14, other OpenShift minors, other binary hashes or a cloud CSI backend. Full unavailable-image/admission-policy variants and the release-wide matrix remain outstanding. The matrix records 4.21 as partial and the release gate continues rejecting incomplete evidence. No release was pushed or published.

Final test resources are retained for inspection; successful migrations end with source recovery and a paused, zero-replica destination. Foreign-object, permission and intentionally manual cases remain as their snapshots describe. The automatic baseline harness cleans up its own resources after success.

The static-volume variant failed its source marker precondition under the legacy SCC and remains a failed attempt. The user subsequently requested a regular volume and authorized the v2 SCC binding only if needed. The [new-app follow-up](new-app-live-tests.md) passed with a dynamically provisioned gp3 CSI PVC under the default `restricted-v2` SCC; no additional SCC grant was needed or applied. The superseded hostPath namespace, legacy binding and PV definition were removed. The original archive and results above remain historical evidence for their recorded binary.
