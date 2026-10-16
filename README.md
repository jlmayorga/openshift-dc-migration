# OpenShift DeploymentConfig migration

Convert exported or live `apps.openshift.io/v1` DeploymentConfigs into baseline `apps/v1` Deployments, review migration plans, and stage or explicitly cut over supported workloads.

The minimum compatibility target remains **OpenShift 4.14 / Kubernetes 1.27**. Newer clusters are accepted through API discovery without an arbitrary maximum version. **Full release qualification remains incomplete.** Actual live tests on **OpenShift 4.21.36 / Kubernetes v1.34.9** cover migration, routing, SCC defaults, triggers, failures and recovery; see the [live evidence](docs/live-tests-2026-10-15.md) and [actual `oc new-app` tests](docs/new-app-live-tests.md), including Docker/S2I rebuilds and a regular CSI volume, for exact results and limits. Fixtures and HTTP simulations remain separate from these cluster observations. See the [qualification matrix](compatibility/matrix.json) and [release requirements](docs/qualification.md). Tool compatibility is separate from Red Hat's support lifecycle for a cluster.

Use Go **1.27.2**:

```sh
go mod download
go mod verify
go build -mod=readonly -trimpath -o /tmp/openshift-dc-converter .
```

Generation is read-only against the cluster and is the default:

```sh
# Exported YAML, JSON, lists or multiple YAML documents; no kubeconfig is loaded.
/tmp/openshift-dc-converter --input exported-dc.yaml --output-dir ./review

# Live generation with namespace-scoped permissions and an explicitly selected context.
/tmp/openshift-dc-converter --projects example --context TEST_CONTEXT --output-dir ./review
```

Offline objects require an explicit namespace. `--projects` deduplicates names and skips reserved namespaces before access checks. `--kubeconfig` and `--context` override standard loading rules; otherwise `KUBECONFIG` and the normal default file apply. Basic conversion never needs namespace-object or cluster-version access. If the DC API is absent or disabled, live discovery fails clearly; exported-file conversion remains available.

Every workload gets a YAML manifest and JSON review plan. Pod labels, including Service-facing `deploymentconfig`, are retained. A separate migration label identifies the destination selector. Explicit zero replicas, pause, readiness/history settings, Rolling/Recreate behavior and supported image triggers are mapped. Unsupported hooks, custom/test behavior, disabled ConfigChange rollouts and ambiguous triggers produce manual diagnostics and block application. Read the [feature mapping](docs/feature-mapping.md).

A generated manifest describes intended replicas. Review it through the staged procedure below before applying it while the DC is active.

```sh
# Inspect dependencies and produce a live plan without cluster writes.
/tmp/openshift-dc-converter --mode plan --projects example --context TEST_CONTEXT \
  --health-url https://APPLICATION/health --output-dir ./review

# Validate admission, then stage a paused destination with zero replicas.
/tmp/openshift-dc-converter --mode dry-run --plan-file ./review/example/web.plan.json --context TEST_CONTEXT
/tmp/openshift-dc-converter --mode stage --plan-file ./review/example/web.plan.json --context TEST_CONTEXT

# Check Pod admission, quota and node capacity while the source stays up.
/tmp/openshift-dc-converter --mode preflight --plan-file ./review/example/web.plan.json --context TEST_CONTEXT

# After pausing other reconcilers/scalers and reviewing the plan:
/tmp/openshift-dc-converter --mode cutover --plan-file ./review/example/web.plan.json \
  --context TEST_CONTEXT --automation-quiesced

# Explicit recovery after inspecting a failed/interrupted migration:
/tmp/openshift-dc-converter --mode rollback --plan-file ./review/example/web.plan.json --context TEST_CONTEXT
```

Cutover reruns the constraint preflight before changing any HPA or source. A refusal leaves the source running and the destination paused at zero. See [pre-retirement constraints](docs/migration.md#constraints-before-source-retirement), including the extra read/dry-run permissions and point-in-time limits, and [actual live-test evidence](docs/preflight-live-tests.md).

The legacy `--apply-changes` now aliases **zero-replica staging** and requires a reviewed `--plan-file`. It cannot create an active duplicate. Exclusive cutover stops and verifies source pods before starting the destination. Overlap requires an explicit planning choice and justification and is blocked for volume/host-bound workloads. The DC is retained for recovery. See the [migration and recovery procedure](docs/migration.md) and [RBAC reference](docs/rbac.md).

The canonical report is `conversion_report.json`; the optional PDF defaults to `conversion_report.pdf` (`--report-path ''` disables it). `--json-report` selects the canonical path; report, log, input and plan paths must be distinct. Reports contain per-source identities, destination, stage, outcome, warnings, unsupported features and errors. Generation, application, readiness, manual cases, failures and skips have separate totals. Exit status is **0** for completed requested operations (including explicit skips/empty namespaces) and **1** for validation, manual migration, conversion, save, admission, apply, readiness, cancellation or report failures. Partial runs retain reports where storage is writable.

Files use atomic replacement with restrictive permissions. Plans contain original manifests and may contain environment values; keep them private. `--request-timeout`, `--operation-timeout` and `--readiness-timeout` bound work. SIGINT/SIGTERM cancel API requests and preserve the journal/report where possible. Lost connectivity or unverified recovery is a failure, never a success.

```sh
go test -mod=readonly -race ./...
go vet -mod=readonly ./...
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s tools -p 'test_*.py'
```

Development details, dependency decisions, vulnerability remediation and actual-cluster setup are in [docs/development.md](docs/development.md), [docs/dependencies.md](docs/dependencies.md), [docs/security.md](docs/security.md) and [docs/qualification.md](docs/qualification.md). Release automation builds checksums and version/provenance metadata, requires actual-cluster evidence for the exact artifacts, and creates a draft for maintainer review. No release is published by this maintenance work.
