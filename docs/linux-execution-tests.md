# Linux execution and scoped live tests

A Linux/amd64 build from `0b64465` was executed in a test Pod on OpenShift 4.21.36 / Kubernetes v1.34.9. The [structured result](../compatibility/evidence/2026-10-16-linux-stage-4.21.36.json) records its binary hash, actual Pod/image identities, permissions and observations. This is a **partial** integration result, separate from the complete [Darwin arm64 scenario run](client-upgrade-live-tests.md).

| Check | Result and scope |
| --- | --- |
| Linux unit and HTTP contracts | 356 tests/subtests passed, 79 top-level tests, no skips; actual Linux execution without race instrumentation. These are fixtures and loopback simulations, not OpenShift integration |
| Offline version fixtures | All nine synthetic 4.14–4.22 fixtures generated successfully with the Linux CLI |
| Actual new-app source | Live generation and planning passed for a real `oc new-app --as-deployment-config` source |
| Server dry run | Accepted without a persisted Deployment |
| Staging and rerun | Owned Deployment created paused at zero replicas; rerun retained its UID and spec |
| Missing permissions | Preflight failed visibly; original source UID, Pod UID and full spec, HPA inventory and Services remained unchanged |

The runner uses the cluster's existing immutable Python and CLI images and a dedicated namespace-only test account. Its executable hashes were checked inside the actual Linux Pod. It receives no admin credentials, source-write permission, Secret read, cluster-scoped role/binding or SCC grant. The first planning attempt correctly reported missing namespaced StatefulSet inventory access; adding the documented read-only controller permissions enabled a fresh plan. That initial refusal is retained.

No Linux cutover, rollout readiness, image-controller update, volume ownership or recovery pass is inferred from this run. The full suite needs additional cluster inventory and fixture permissions. The proposed grant has not been applied. OpenShift 4.22 and the remaining platform binaries also lack full actual qualification. These results do not open the release gate.

Raw commands, reports, fixture results, test output and exact private runner helpers are retained in the session evidence archive. Credential profiles, the private upload payload and executable files are excluded. An initial helper-source parsing error is recorded separately from the successful completion; no cluster command ran in that helper attempt.

The private archive `.scratchpad/linux-qualification-20261016/openshift-4.21.36-linux-stage-evidence.tar.gz` has SHA-256 `d49a77bc1c4d84cb8b9bf3e7f3c55870d488d1081360af550eba43abef0f72e7`. All 237 manifest entries verified, and the actual admin/runner credential values were absent. Rebuilding its saved source reproduces the executed Linux binary byte for byte. Archive/rebuild metadata is appended after sealing to avoid circular checksums.
