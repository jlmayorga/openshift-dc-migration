# OpenShift qualification and release checklist

The [compatibility-review backlog](backlog.md) records the planned release-note review for unavailable OpenShift 4.14–4.20 clusters. Its outcome will support an explicitly labelled compatibility assumption and a corresponding qualification-policy update, while retaining actual integration results as separate evidence.

OpenShift 4.14 / Kubernetes 1.27 remains the mandatory floor. The current released-minor inventory is 4.14 through 4.22, with a separate next-development smoke row. The newest GA inventory source is [Red Hat's 4.22 announcement](https://developers.redhat.com/articles/2026/07/14/whats-new-developers-red-hat-openshift-4-22). On 2026-10-15, Red Hat's public [stable-4.22 update graph](https://api.openshift.com/api/upgrades_info/v1/graph?channel=stable-4.22&arch=amd64) contained 4.22.18 and stable-4.23 had no nodes. This observes release availability, not tested support. Refresh the inventory before release; conversion discovers capabilities and imposes no maximum version.

**Fully qualified versions: none.** Actual tests have now run on **OpenShift 4.21.36 / Kubernetes v1.34.9**; see the [live test evidence](live-tests-2026-10-15.md) and [actual new-app tests](new-app-live-tests.md) for exact scope, binary hashes, failures and retained artifacts. This tests its own version and cannot supply 4.14 evidence. The matrix records exact patches/Kubernetes versions only after observation. Synthetic fixtures for each target, the Kubernetes 1.27 baseline catalog and loopback JSON contracts are separate local evidence. None proves OpenShift admission, SCC defaults, image controllers or Service routing.

The current native artifact additionally passed [26 constraint/migration cases](preflight-live-tests.md) and [15 runtime/recovery cases](recovery-live-tests.md). Its successful quota preflight and actual runtime-failure recovery are separate evidence. The matrix remains partial until all required targets, artifacts and scenarios pass.

## Running on a disposable cluster

The baseline driver provisions no cluster, node or PVC. The separately authorized new-app volume case creates a 1Gi claim using an existing StorageClass. Supply a kubeconfig, explicit authorized context, existing disposable `dcmig-*` namespace, and immutable approved HTTP images that listen on port 8080. A cluster administrator/operator must label that namespace `migration.openshift-dc.io/test-namespace=true`; this integration-only namespace read is not required for normal conversion. Reserve the resource name `web`; the harness refuses to overwrite existing objects. The context must permit the integration operations in the RBAC reference and access to ClusterVersion metadata. It also needs network access to the test Route.

```sh
python3 tools/qualify_openshift.py \
  --kubeconfig "$TEST_KUBECONFIG" --context "$TEST_CONTEXT" \
  --namespace "$TEST_NAMESPACE" --binary "$CANDIDATE_BINARY" \
  --image "$APP_IMAGE_DIGEST" --second-image "$SECOND_APP_IMAGE_DIGEST" \
  --health-path / --output ./qualification-result.json --allow-test-mutations
```

The command creates test workloads and an HTTP Route, temporarily removes/recreates a test HPA, scales controllers, tests Pod quota failure, sends SIGTERM to an active migration, and exercises explicit recovery. It cleans up only objects bearing this run's ownership label after successful scenarios. Failed runs retain objects and private artifacts for inspection; never broadly delete a namespace or recover an uncertain workload automatically. Inspect the journal and use rollback before cleanup.

Automatic scenarios include baseline dry run without persistence, SCC annotation/default evidence, source/destination readiness, Service endpoint routing, exclusive and overlap cutover, HPA retargeting, stale plans, safe reruns, quota refusal before any source/HPA mutation, interruption/recovery and actual ImageStream updates (the latter requires the second image). Both images must work with the same port/health contract. Image tags are restored before rollback so source recovery is reproducible.

`--smoke-only` runs the basic exclusive scenario. Its successful exit records **partial** evidence, which cannot pass the release gate. Full invocations also remain partial when any required variant is missing. Every actual record includes binary SHA-256, source revision, fixture tree hash, hashes of go.mod/go.sum, actual Kubernetes module versions, exact OpenShift/Kubernetes versions, release image, date and runner/run provenance. The harness has executed all 13 automatic scenarios on the authorized 4.21.36 throwaway cluster. Its summary remains partial; additional live drivers and remaining qualification limits are recorded in the linked test evidence.

## Required variants and evidence

The following require actual test-cluster inputs or controlled fault/credential profiles. They cannot be inferred from loopback tests. Complete each on every required target/artifact; preserve command logs and journal/report snapshots. A reviewer must confirm these results before supplying supplemental records.

| Scenario ID | Reproducible qualification steps and required observation |
| --- | --- |
| constraint-preflight | After staging, impose namespace/shared quota, LimitRange, SCC or placement restrictions while the source is healthy. Both preflight and cutover must fail with diagnostics before HPA/source mutation, retain a staged zero-replica destination and preserve source availability/traffic. Repeat with constraints changed after a passing preflight. |
| exclusive-volume | Use an explicitly approved test PVC in the disposable namespace; the new-app volume driver can create a regular 1Gi claim on an existing StorageClass. Mount it in a one-replica DC, write a unique test marker, plan exclusive cutover, then verify source RCs/pods reach zero before destination starts, marker persists, readiness succeeds, and rollback restores source after destination termination. Dynamic provisioning is limited to a separately authorized regular-PVC run. |
| singleton-worker | Use an approved worker fixture with a visible active-owner heartbeat. Choose exclusive mode. Observe one active worker throughout retirement/startup and recovery; repeat failure/cancellation. Selecting overlap for a singleton is not supported. |
| restricted-rbac | Bind a lab-only account to DC list plus discovery/SSAR; generation must succeed without namespace or version reads. Deny DC list, Deployment create, source update and HPA mutation separately; each selected operation must fail visibly in exit status/report before unauthorized writes. Test expired credentials too. |
| admission-rejection | Use lab quota/admission policies to deny destination create/update and SCC-constrained pods. Verify dry-run failure creates nothing; controller-time rejection never reports ready; source recovery is verified. Exercise namespace-provided UID/FSGroup defaults and an unavailable image. Restore only policies created for this case. |
| cross-namespace-image-triggers | Use an explicitly approved second image namespace. Automatic ImageChange must update the named destination container from that namespace; disabled/paused triggers must remain paused. Verify source triggers cannot recreate old workloads during cutover. |
| multi-container-image-triggers | Use two approved containers with distinct ImageStreamTag targets. Update each tag separately; only its named container changes. Ambiguous/missing-container/unsupported references must block application. |
| per-mutation-recovery | Interrupt or deny the next request after each journalled HPA deletion, source trigger pause, source retirement, destination activation and HPA recreation. Roll back each state. Re-run recovery; foreign objects/changed specs and orphan destination pods must block unsafe restoration. Restore dependent specs and verify routing and health. |
| connectivity-loss | Revoke only the lab account or use a controlled network proxy after each mutation. CLI must return failure and retain uncertain state. Restore access, inspect cluster/journal, and explicitly recover. Never count inaccessible or merely configured recovery as passed. |

Also run the supported fixture corpus through actual server dry run and exercise Recreate, percentage/integer Rolling limits, zero replicas, pause/test/custom/hooks, disabled ConfigChange behavior and source immutability. Unsupported cases must remain manual and blocked. Keep negative/missing-API behavior under the contract tests; where a later cluster disables DCs, record that live capability limitation and verify offline conversion explicitly.

`--supplemental-evidence FILE` accepts an array of actual variant records. Each must have `kind: actual-openshift`, `result: passed`, `scenario`, `testedAt`, `runURL`, and exactly matching `artifactSHA256`, `revision`, `fixtureSHA256`, `dependencySHA256`, `openshiftPatch`, `kubernetesVersion` and `releaseImage` from this candidate/cluster. Mismatched or stale records are rejected. Supplying labels without performing these tests is not qualification. The automatic summary remains partial until every required scenario is present.

## Trusted CI and release gate

Protect the `openshift-qualification` environment and use trusted self-hosted runners labelled `openshift-qualification` and their platform (`linux-amd64`, `linux-arm64`, `darwin-amd64`, `darwin-arm64`, `windows-amd64`). There are no PR-triggered cluster jobs. Main pushes exercise floor/newest baseline targets; weekly/manual jobs cover all released minors/platforms and the next-development smoke target. No missing context or runner is counted as passing.

Configure:

- `OPENSHIFT_TEST_KUBECONFIG` environment secret: test-only kubeconfig contents.
- `OPENSHIFT_TEST_TARGETS_JSON` secret: `{ "4.20": { "linux-amd64": { "context": "AUTHORIZED_CONTEXT", "namespace": "dcmig-420-linux-amd64", "supplementalEvidence": "/private/qualified-variants.json" } } }`, with each required minor/platform configured separately. The optional supplemental path is private data preloaded on that trusted runner.
- Immutable `OPENSHIFT_TEST_IMAGE` / `OPENSHIFT_SECOND_TEST_IMAGE` variables and `OPENSHIFT_CANDIDATE_RUN_ID` for scheduled runs. `oc`, Python 3 and Route connectivity must be installed on runners.

First run Build and Release to create candidate binaries and provenance. It deliberately blocks without qualification. Run OpenShift qualification against that candidate run ID; the job checks out the candidate revision, not an arbitrary current branch. Set `OPENSHIFT_QUALIFICATION_RUN_ID` to the successful qualification run and re-run the release qualification job for the same candidates. Evidence is downloaded from the trusted run; committing evidence would change the revision/binary and is not a substitute.

Before creating/publishing a release:

- Local and CI race tests, vet, formatting, readonly builds, module verification/clean-module checks, Python gate tests and pinned security scanners must pass. Preserve scan artifacts; inspect dispositions.
- Every required released-minor row must contain full passing **actual** results for each shipped binary hash. Record exact patches, actual Kubernetes version, aligned module versions, test dates, known limitations and provenance; 4.14 must report Kubernetes 1.27.
- Fresh full-matrix qualification is mandatory after dependency/fixture/binary changes. The evaluated Kubernetes 0.37.1 candidate cannot replace 0.31.3 until 4.14 integration passes and the full matrix is refreshed.
- Review feature/RBAC/recovery docs and all variant evidence; refresh the released-minor inventory. Do not advertise an untested release or an untested feature as verified.
- Release automation creates only a draft after gates pass, attaching binaries, checksums, build metadata, scans and compatibility results. A maintainer reviews before publishing; this work does not push tags or publish anything.

## Additional live variant drivers

`tools/qualify_configurations.py`, `tools/qualify_features.py`, `tools/qualify_permissions.py` and `tools/qualify_recovery.py` accept explicit `--kubeconfig`, `--context`, `--binary`, immutable `--image`, disposable `--namespace-prefix dcmig-...`, private `--output` and `--allow-test-mutations`. Features also requires `--second-image`. These drivers create new labelled namespaces and retain workloads, source snapshots, stage reports, plans, API observations and command evidence for inspection. Use unique prefixes; they refuse to adopt an existing namespace. An empty case selection fails. `--cases` selects named configuration, image or permission cases where supported.

The recovery driver uses a loopback-only authenticated oc proxy and a credential-free fault proxy to lose a success response after each of six real cluster mutations. Authorization/SCC reviews and server dry runs do not advance its mutation counter. It then restores connectivity and verifies explicit rollback and reruns. The permission driver saves short-lived account tokens privately; never publish its `*-credentials.json` files or kubeconfigs.

Explicitly select permission case `--cases expired-credentials` to test real expiration of a server-issued ten-minute token. It verifies generation succeeds before expiration and fails visibly afterwards without changing source identity/spec/health or creating a destination. The optional case waits for the actual signed expiry within `--expiry-wait-limit` (default 720 seconds); it does not revoke the account, forge a token or change clocks. It is excluded from the default permission run.

The optional feature `--static-volume-test --cases persistent-volume --volume-scc SCC_NAME` needs separate explicit approval for the node directory and exact existing SCC binding. It requires the explicitly approved existing `hostmount-anyuid-v2` SCC and creates a static hostPath PV/PVC with an empty StorageClass and uses an explicitly selected test SELinux context; it provisions no cloud volume. The test must first prove the source can write/read its marker. A namespace SCC grant does not imply that an admin-created Pod dry run reproduces service-account/controller admission. Inspect actual Pod admission and source readiness before migration. This single-node test cannot qualify cloud CSI attach/detach behavior.

## Apps created with oc new-app

`tools/qualify_new_app.py` creates actual applications with `oc new-app --as-deployment-config`, including existing/cross-namespace ImageStreams, grouped containers, processed templates, and binary Docker/S2I build workflows. It verifies that rebuild output continues updating the Deployment after cutover, and retains BuildConfigs, ImageStreams, Services, UIDs and per-stage observations. Both Service ports are exercised where the runtime exposes 8080 and 8888. The default builder input is `openshift/httpd:2.4-ubi9`; choose `--builder-image-stream` for another installed, approved S2I builder. Build evidence records resolved images.

Supply the usual explicit context/kubeconfig/binary and new `dcmig-*` namespace prefix, plus immutable `--image`, `--second-image` (HTTP 8080), and `--sidecar-image` (HTTP 8081). Select `--cases all` or comma-separated case names. Full runs include a regular 1Gi PVC and require explicit `--allow-regular-pvc`; it uses the existing default StorageClass and the default workload SCC, without granting a new SCC. The suite creates test PVCs only when that case is selected and authorized. It does not create nodes or StorageClasses. Keep regular test storage only as long as needed for inspection.

For example, with approved disposable targets and images:

```sh
python3 tools/qualify_new_app.py \
  --kubeconfig "$TEST_KUBECONFIG" --context "$TEST_CONTEXT" \
  --binary "$CANDIDATE_BINARY" --namespace-prefix dcmig-newapp-run \
  --image "$HTTP_8080_DIGEST" --second-image "$SECOND_HTTP_8080_DIGEST" \
  --sidecar-image "$HTTP_8081_DIGEST" --output ./private-newapp-evidence \
  --cases all --allow-test-mutations --allow-regular-pvc --timeout 240
```

Apps awaiting their first binary build can generate read-only review artifacts, but cannot produce an executable live plan until the DC image controller has resolved their automatic ImageChange triggers. A new-app origin does not bypass unsupported hooks/test/custom behavior, selector/ownership checks or stale dependency checks.

The `failed-initial-rollout` case deliberately exits with code 42 only when the destination migration label is present. Its source is healthy and all pre-retirement admission/capacity checks pass; actual startup fails after source retirement. The runner verifies a failed, non-ready result, retained recovery state, and explicit source/HTTP restoration. This proves a runtime failure path that quota preflight cannot replace. The feature runner's `multi-image` fixture uses a sleeping sidecar so independently updating both tags does not depend on configurable HTTP ports.

## Multiple namespaces in one invocation

The [actual multi-namespace evidence](multiple-namespaces-live-tests.md) records six passing cases on OpenShift 4.21.36.

`--projects namespace-a,namespace-b,namespace-c` supports generation and live planning in one invocation. Artifacts are written under each namespace directory even when every DC has the same name. Duplicate names in the project list are processed once. Empty, skipped, failed and manual projects/workloads retain separate report entries; any failure still returns a nonzero aggregate exit while later namespaces are processed.

Run `tools/qualify_namespaces.py` to verify this against actual `oc new-app --as-deployment-config` workloads. It creates six labelled namespaces: three same-named apps with distinct HTTP responses and replica counts 1/2/0, an unselected sentinel app, an unsupported test DC and an empty namespace. It checks multi-project generation/planning, nonpersistent dry runs of those plans, manual and RBAC partial failures, and isolated cutover/recovery in different orders. A test-only reader is granted DC-list permission in two namespaces and denied the third; its short-lived credential stays private.

```sh
python3 tools/qualify_namespaces.py \
  --kubeconfig "$TEST_KUBECONFIG" --context "$TEST_CONTEXT" \
  --binary "$CANDIDATE_BINARY" --image "$HTTP_8080_DIGEST" \
  --namespace-prefix dcmig-namespaces-run --output ./private-namespace-evidence \
  --allow-test-mutations --timeout 120
```

Dry run, stage, cutover and rollback each execute one reviewed `--plan-file`. The multi-namespace tests create separate health-bound plans for execution so every active app is checked through its own Route, and verify all other namespace states after every cutover/recovery. The runner retains resources and command/report/snapshot evidence, provisions no storage and grants no cluster role. Never publish its `*-credentials.json` file or kubeconfig.

## Pre-retirement constraint driver

`tools/qualify_constraints.py` uses the same explicit target/binary/image, unique `dcmig-*` prefix, private output and mutation acknowledgement as the other live drivers, plus `--cases all` or a comma-separated selection. It creates actual new-app sources and fixed-size source HPAs, stages at zero, then installs constraints. The negative cases verify both standalone preflight and cutover refusal, unchanged source spec/availability/HTTP and original HPA UID/spec, zero destination replicas and a still-staged journal. Positive and zero cases exercise cutover/recovery.

The shared-cluster-quota case creates one ClusterResourceQuota with a unique label selector matching only its two lab namespaces, then removes that quota. The SCC case grants existing `anyuid` to one namespace test account to start its source, removes the grant, and verifies SCC refusal while the original Pod stays healthy. It does not change an SCC, use hostPath, provision storage or modify nodes. Node-capacity/selector failures use only lab namespace LimitRanges/annotations. Runtime-label NetworkPolicy and disruption-budget cases exercise namespace availability constraints. The absolute-budget overlap case is retained at a healthy completed cutover rather than automatically rolling back through a stop-first recovery. These test-setup privileges do not belong to the converter.

The automatic baseline now records its quota case as `constraint-preflight`; it cannot count a pre-retirement rejection as an actual `failed-rollout`. Failed rollout remains a required release scenario with separate actual evidence. Evidence for an older binary remains historical and cannot qualify this new gate.
