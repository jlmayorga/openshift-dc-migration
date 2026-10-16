# OpenShift DeploymentConfig migration

Convert exported or live `apps.openshift.io/v1` DeploymentConfigs into baseline `apps/v1` Deployments, review migration plans, and stage or cut over supported workloads. This CLI is for OpenShift application operators coordinating a controlled migration and recovery procedure.

## Build and try offline conversion

From a checkout, use **Go 1.27.2** and Git. Maintainer Python tools require **Python 3.12 or later**. Building needs dependency-download access; conversion of exported input needs no cluster, `oc`, kubeconfig or credentials.

```sh
go mod download
go mod verify
DEMO_DIR=$(mktemp -d)
go build -mod=readonly -trimpath -o "$DEMO_DIR/openshift-dc-converter" ./cmd/openshift-dc-converter
"$DEMO_DIR/openshift-dc-converter"   --input internal/migration/testdata/ocp-4.14-dc.yaml   --output-dir "$DEMO_DIR/review"   --json-report "$DEMO_DIR/conversion_report.json" --report-path ''
```

This supplied fixture is synthetic. Success writes `review/example/web.yaml`, `review/example/web.plan.json` and the JSON report under `$DEMO_DIR`. It does not deploy the fixture image. Exported YAML, JSON, lists and multiple YAML documents are accepted; every input object needs a namespace. Offline plans lack live dependency inventory and cannot execute a migration.

## Live workflow

Generation is the default and reads DeploymentConfigs without changing workloads. Live `plan` also inventories dependencies. `dry-run` validates a paused, zero-replica destination without persistence; `stage` creates that destination. `preflight` assesses admission, quota, policy and placement before retirement. `cutover` changes HPAs and source/destination workloads and can interrupt service. The default exclusive cutover verifies source Pods have stopped before activating the destination. `rollback` is explicit recovery and also changes workloads; it stops the destination before restoring an exclusive source.

Select a context explicitly and follow the [migration and recovery procedure](docs/migration.md) with [permissions for each mode](docs/rbac.md). Coordinate external reconcilers and scalers before cutover. The legacy `--apply-changes` flag aliases zero-replica staging and requires a reviewed plan.

## Capabilities and limits

Labels, supported Rolling/Recreate behavior, replica settings and supported ImageChange triggers are preserved. Hooks, custom/test deployment semantics, ambiguous triggers and unfamiliar fields require manual migration. See [conversion coverage](docs/feature-mapping.md). Preflight is a point-in-time assessment; it does not reserve capacity or guarantee image pulls, CSI mounts, application readiness or every network path.

The minimum compatibility target is OpenShift 4.14 / Kubernetes 1.27. Historical source assessments support conditional 4.14–4.20 assumptions, and exact historical Linux amd64 and Darwin arm64 binaries completed actual qualification on OpenShift 4.21.36 / Kubernetes v1.34.9. **All current maintenance binaries need reassessment and new qualification.** OpenShift 4.22 and other native platforms remain untested. [Compatibility](docs/compatibility.md) explains these distinct evidence bases; tool compatibility is separate from Red Hat's cluster support lifecycle.

Plans and reports can contain sensitive workload values; keep them private. The canonical report is JSON; `--report-path ''` disables optional PDF output. Unsupported/manual cases and operation or report failures return nonzero status.

## Further guidance

- [Migration, preflight and recovery](docs/migration.md) · [RBAC](docs/rbac.md)
- [Feature mapping](docs/feature-mapping.md) · [Compatibility prerequisites](docs/compatibility-review.md)
- [Development and tool commands](docs/development.md) · [Dependencies](docs/dependencies.md) · [Security](docs/security.md)
- [Developer-run live qualification](docs/qualification.md) · [Historical release verification](docs/release-scope.md) · [Follow-up backlog](docs/backlog.md)
