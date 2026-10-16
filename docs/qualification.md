# Optional OpenShift qualification

Ordinary builds, Python tests, Go tests and security checks do not require OpenShift. Live qualification is an explicit development operation against an authorized disposable target. It can create, scale and interrupt test applications; it provisions no cluster and must never select the operator's current kubeconfig implicitly.

[Compatibility](compatibility.md) summarizes completed historical results and untested targets. Exact historical Linux amd64 and Darwin arm64 binaries qualified on OpenShift 4.21.36 / Kubernetes v1.34.9. All changed current binaries need reassessment and new actual evidence; historical passes do not transfer. The wider full-matrix gate remains incomplete. Qualification, candidate builds and [historical release verification](release-scope.md) are separate operations.

## Select a development target

Developers run qualification from their own machines against a development cluster. GitHub Actions runs ordinary checks, candidate builds and explicit historical verification; it does not connect to development clusters or run live qualification.

Use the source checkout corresponding to the candidate being tested. Verify its `build-provenance.json` and `SHA256SUMS`, including the source, dependency and fixture identities. Run the exact binary on its matching native operating system and architecture; a cross-build or emulated execution does not establish native qualification. Install the matching `oc` client locally and confirm that your machine can reach the cluster API and application Routes.

Choose an explicit test kubeconfig file, context and disposable namespace. Keep credentials and generated evidence in private local storage. No GitHub cluster secrets, runner setup or dispatch configuration is required. The baseline invocation may produce partial evidence until the required supplemental actual variants are present; review the results before using them for release verification.

## Run a driver directly

Use Python 3.12+, a matching `oc` client on PATH, a native candidate binary, explicit kubeconfig/context, approved immutable images and target-specific fixture permissions. The baseline driver needs an existing disposable `dcmig-*` namespace labelled `migration.openshift-dc.io/test-namespace=true`; an operator supplies this namespace. Reserve resource name `web`. Other drivers create fresh labelled namespaces from a unique `--namespace-prefix dcmig-...` and refuse to adopt existing ones. The operator needs the harness permissions described in [RBAC](rbac.md), including release metadata access and Route connectivity; these exceed ordinary converter access.

Set `TEST_KUBECONFIG` to the explicit kubeconfig file path, `TEST_CONTEXT` to the development context, `TEST_NAMESPACE` to that labelled disposable namespace and `CANDIDATE_BINARY` to the matching native binary. Set both image variables to approved immutable HTTP image references. After separately authorizing those test mutations, run:

```sh
python3 tools/qualify_openshift.py \
  --kubeconfig "$TEST_KUBECONFIG" --context "$TEST_CONTEXT" \
  --namespace "$TEST_NAMESPACE" --binary "$CANDIDATE_BINARY" \
  --image "$APP_IMAGE_DIGEST" --second-image "$SECOND_APP_IMAGE_DIGEST" \
  --health-path / --output .scratchpad/qualification/actual.json --allow-test-mutations
```

It creates test workloads and a Route, changes test HPA/controller state, checks quota refusal, interrupts a migration and verifies explicit recovery. Successful baseline scenarios clean up only owned fixture objects. Failed runs and several supplemental drivers retain resources and private artifacts for inspection. Inspect journal and cluster state before rollback or cleanup; never broadly delete a namespace or restore an uncertain workload automatically.

Automatic coverage includes nonpersistent dry run, admission/SCC observations, source/destination readiness, Service endpoints, exclusive/overlap cutover, HPA retargeting, stale plans, safe reruns, interruption/recovery and ImageStream updates. `--smoke-only` intentionally produces partial evidence. A full invocation also remains partial until all policy-required actual variants are present. A successful command exit alone does not establish release eligibility.

## Driver inventory

Qualification maintainers consume every driver below for an explicit scenario family. Run the shown safe command to inspect current flags, then supply the target/image/output and mutation arguments described above. Except for the baseline's `--namespace`, drivers use a fresh `--namespace-prefix`. Case selection and additional inputs differ; an empty selection is an error.

| Command | Purpose and additional prerequisites |
| --- | --- |
| `python3 tools/qualify_openshift.py --help` | Baseline actual integration and supplemental-record assembly; existing labelled namespace, optional second image |
| `python3 tools/qualify_configurations.py --help` | Recreate/Rolling, replica, configuration and unsupported-semantics fixtures |
| `python3 tools/qualify_features.py --help` | Image triggers, containers and optional volume features; requires second image; special storage fixtures need separate approval |
| `python3 tools/qualify_permissions.py --help` | Restricted RBAC, denial and optional real signed-token expiration; saves credentials privately |
| `python3 tools/qualify_recovery.py --help` | Per-mutation uncertain-response/fault recovery using loopback authenticated and fault proxies |
| `python3 tools/qualify_new_app.py --help` | Actual new-app, templates, image imports/builds, failed startup and orphan recovery; HTTP/sidecar images, installed builder and optional approved regular PVC |
| `python3 tools/qualify_namespaces.py --help` | Multi-project isolation, aggregate failures, same-named resources and independent recovery; namespace reader test identity |
| `python3 tools/qualify_constraints.py --help` | Quota/SCC/capacity/PDB/policy refusal before retirement; isolated shared quota and namespace SCC-use setup where selected |
| `python3 tools/qualify_storage.py --help` | Removed/disabled inline storage refusal using zero-replica sources; creates no PV/PVC or actual legacy mount |
| `python3 tools/qualify_admission.py --help` | Required-SCC service-account authorization revoke/refuse/restore; requires `--allow-scc-binding` and existing policy inspection |
| `python3 tools/qualify_raw_fields.py --help` | Unfamiliar persisted Deployment field refusal and raw-preserving recovery; server must retain fixture field |
| `python3 tools/qualify_admin_policy.py --help` | ANP/BANP subject/peer/future-hash selector checks; requires `--allow-cluster-policy-fixture`, never replaces an existing BANP |
| `python3 tools/qualify_conflicts.py --help` | Source resource-version churn, full-spec drift refusal and bounded-conflict recovery with controlled proxy faults |
| `python3 tools/qualify_worker.py --help` | Singleton success/startup-failure/cancellation through Lease ownership; approved image needs `/bin/sh`, `curl`, `sed`, `date`, `sleep` and `/hello_openshift` |

Some drivers support `--previous-binary` solely for read-only regression preflight, never an old-binary cutover. Permission `--cases expired-credentials` waits for an actual server-issued token to expire; it is excluded from default cases and bounded by `--expiry-wait-limit`. It changes neither clocks nor token signatures.

The new-app driver uses `oc new-app --as-deployment-config`, grouped containers, processed templates and binary Docker/S2I builds. `--builder-image-stream` selects an installed approved builder. `--cases all --allow-regular-pvc` includes an explicitly authorized regular claim through an existing default StorageClass. It creates neither nodes nor StorageClasses and grants no extra SCC for that regular-volume case. Resolve first-build ImageChange triggers before live migration.

The feature driver's optional `--static-volume-test --cases persistent-volume --volume-scc SCC_NAME` requires separate approval for an existing node directory, exact SCC binding and SELinux context; it creates a static hostPath PV/PVC. It cannot qualify cloud CSI behavior. Constraint/admission fixtures may create namespace-only grants to existing SCCs; the converter itself never grants access or changes SCCs. Admin-policy fixtures require separate cluster policy mutation privileges and ownership-checked cleanup. These permissions are test setup, not converter requirements.

## Required variants and evidence

Actual cluster inputs or controlled fault profiles are needed for the following policy variants. Preserve private logs and journal/report snapshots, and review results before supplying supplemental records. Local HTTP simulations cannot substitute.

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

Also run the synthetic fixture corpus through actual server dry run and exercise Recreate, percentage/integer Rolling limits, zero replicas, pause/test/custom/hooks, disabled ConfigChange behavior and source immutability. Unsupported cases must remain manual and blocked. Keep negative/missing-API behavior under the contract tests; where a later cluster disables DCs, record that live capability limitation and verify offline conversion explicitly.

`--supplemental-evidence FILE` accepts an array of actual variant records. Each must have `kind: actual-openshift`, `result: passed`, `scenario`, `testedAt`, `runURL`, and exactly matching `artifactSHA256`, `revision`, `fixtureSHA256`, `dependencySHA256`, `openshiftPatch`, `kubernetesVersion` and `releaseImage` from this candidate/cluster. Mismatched or stale records are rejected. Supplying labels without performing these tests is not qualification. The automatic summary remains partial until every required scenario is present.

## Evidence and release boundary

Actual records bind the binary bytes, source revision, production/dependency/fixture identities, native platform, observed OpenShift/Kubernetes versions and release image. Their test time and execution provenance remain in the external evidence bundle. Skipped, simulated, stale, mismatched or incomplete records cannot qualify a release. Synthetic fixtures generated by `qualification_fixtures.py` are only unit-test inputs and must never be exported as actual evidence.

Review every scenario and limitation before promoting a result. Fresh source-bound review is needed for 4.14–4.20 assumptions after relevant input changes; selected actual targets require final-artifact integrations. Update support policy only as a separately reviewed scope change. Follow [release verification](release-scope.md) for trusted external bundle integrity, frozen reproduction and manual draft preparation. Qualification does not authorize pushing, tagging or publication.
