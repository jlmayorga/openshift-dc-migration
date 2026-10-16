# Source conflict retries and live tests

The later [full Linux qualification](linux-execution-tests.md) passed all 22 required groups on the same `cdbd5358…` binary, plus 381 Linux unit/contract tests and six multi-namespace cases. This report retains the earlier targeted session and its original scope.

Source DeploymentConfig updates now make at most five attempts per transition, with cancellation-aware waits of 100, 200, 400 and 800 ms. Each attempt fetches the current object, checks its original UID and deletion state, compares its entire spec with the spec pinned on the first attempt, and repeats server-side dry-run validation before the persistent update. Only an explicit API conflict permits another attempt. Status/controller updates can change `resourceVersion` without changing the workload spec; these are the conflicts the loop can safely tolerate. A changed spec, even another valid migration state, stops the operation.

Timeouts, EOFs, non-conflict errors and an exhausted attempt limit leave the journal on the explicit recovery path. An uncertain write may already have committed. The dynamic client's PUT requests also disable client-go's implicit `Retry-After` replay: a regression reproduced eleven HTTP PUTs after a committed update before this guard, and exactly one afterward. The strict initial staged-plan freshness check remains. Destination Deployment and HPA conflicts retain their existing recovery behavior. See [migration recovery](migration.md).

The final candidate is source `eb8fb9d6be6274f059211c913191345ab25a9cb1`, built with Go 1.27.2 and Kubernetes libraries v0.37.1. Its production source SHA-256 is `fbce666721a53fcb5c61dd268ec88b2ebb32daec8a71a8a6026efe73fbfedd19`. All live results below belong to that candidate on the authorized disposable **OpenShift 4.21.36 / Kubernetes v1.34.9** cluster. Earlier candidate outcomes remain separate.

| Executed artifact | SHA-256 | Coverage |
| --- | --- | --- |
| Darwin arm64 | `158b35236ef40ed00d5a4ad705a76979ab69aa55791089903e1bd4f3428f7d17` | Full required scenario suite plus 16 source conflict/revalidation/recovery cases |
| Linux amd64 | `cdbd5358de223a8120e5defdb415813e54250b0d368a3d02455cce4f5913e02c` | Seven targeted conflict/uncertain-write cases and three regular CSI migrations; separate from the historical full Linux suite |

The conflict driver changes the real DeploymentConfig through the API between the converter's read and update, then forwards the original stale update. The resulting HTTP 409 comes from the API server. It exercises transient conflicts at dry-run and persistent-write stages, five-conflict exhaustion, deep spec changes, changes to another migration state, replacement UID and cancellation during backoff. UID replacement uses a zero-replica fixture. Guard failures leave the changed resource intact and require explicit recovery.

For the three uncertain-write cases, the real source write succeeds before its response is replaced with EOF, a timeout response, or a timeout response carrying `Retry-After: 0`. Request traces show no automatic follow-up from the failed converter invocation. Explicit rollback and a rollback rerun restore the full original source spec. The full native suite separately withholds responses after each of six persistent migration mutations and verifies recovery.

The [full native result](../compatibility/evidence/2026-10-16-source-conflicts-4.21.36.json) records all 22 required scenario groups. The [targeted Linux session result](../compatibility/evidence/2026-10-16-source-conflicts-linux-4.21.36.json) remains explicitly partial; the later full run supplies separate complete evidence. Each Linux volume repetition encountered one natural dry-run resource-version conflict and passed with one automatic retry. The three repetitions captured 70, 73 and 70 Pod samples, with zero read errors or observed simultaneous volume owners.

The tests use multiple namespaces and include `oc new-app --as-deployment-config` runtime cases, cross-namespace and multiple-container image triggers, denied permissions/quota admission, worker success/startup failure/cancellation, and failed initial rollout/orphan recovery. Both platforms run three consecutive cutovers on the existing regular CSI application, retaining the source/PVC/PV UIDs, original application marker and a fresh data canary through migration and rollback. Pod and Lease samples supplement source-drain checks; they cannot prove every instant or all storage drivers.

Initial failures are retained alongside recovery and fresh test runs. On the final native candidate, one fixture creation and one worker heartbeat observation encountered TLS handshake timeouts. The worker had completed rollback; its complete source spec and paused destination were verified before retiring the fixture. Fresh isolated cases supplied passing evidence. A preliminary candidate also hit a destination Deployment conflict during image-trigger rollback; explicit recovery is recorded. A private fixture adapter waits for the destination controller's version to settle before feature-test rollback. It adds no production retry behavior.

Local validation passed 381 Go tests/subtests with the race detector (83 top-level tests, 77.6% coverage), `go vet`, module verification, an empty tidy diff, 24 Python harness tests, vulnerability checks and a gosec scan of all 20 production files with zero findings. The regression failures before each fix are retained.

The temporary Linux namespace/cluster grants, runner and account were removed after exporting the evidence, with UID checks and absence verification. Existing application storage and the earlier staging workload are preserved. No new storage or SCC modification is used. The older [4297 full Linux result](../compatibility/evidence/2026-10-16-linux-full-4.21.36.json) belongs to a different executable. The later [current full Linux result](linux-execution-tests.md) qualifies the recorded `cdbd5358…` artifact for all 22 scenarios.

OpenShift 4.14–4.20 retain separately source-bound release-review assumptions, without actual older-cluster integration passes. OpenShift 4.22 and the remaining shipped platforms still require qualification; the full release gate remains closed.

The private raw archive is `.scratchpad/source-conflicts-final-20261016/openshift-4.21.36-source-conflicts-evidence.tar.gz`, SHA-256 `2f6f4f85a529f45c2c546976a5d3dbe6c6d2153adac09f935b272ca6a1c0d7f7`. All 7134 manifest entries verified, and actual credential values were checked before export. Its saved Go source reproduces both tested binaries byte for byte. Archive/rebuild metadata was appended after sealing to avoid circular checksums.

To repeat the dedicated cases against another explicitly authorized disposable cluster, use a unique namespace prefix and an immutable compatible qualification image:

```sh
python3 tools/qualify_conflicts.py \
  --binary ./converter \
  --kubeconfig /path/to/test-kubeconfig --context test-cluster \
  --image "$QUALIFICATION_IMAGE" \
  --namespace-prefix dcmig-conflicts-unique \
  --allow-test-mutations --output /path/to/evidence
```

The checked-in driver retains its fixtures and evidence for inspection. The recorded runs additionally used archived private wrappers to retire passed namespaces after UID/state checks and to repeat migrations on the existing CSI volume.
