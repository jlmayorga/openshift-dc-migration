# Development and maintained tools

Run commands from the repository root with Go 1.27.2, Python 3.12+ and Git. Dependency downloads and vulnerability databases may require network access. Ordinary checks need no OpenShift cluster, `oc`, credentials or historical run evidence.

## Local checks

```sh
go mod download
go mod verify
BUILD_DIR=$(mktemp -d)
go build -mod=readonly -trimpath -o "$BUILD_DIR/openshift-dc-converter" ./cmd/openshift-dc-converter
go test -mod=readonly -race ./...
go vet -mod=readonly ./...
python3 -B -m unittest discover -s tools -p 'test_*.py'
gofmt -l cmd internal
go mod tidy
git diff --exit-code -- go.mod go.sum
go -C tools/security mod tidy
git diff --exit-code -- tools/security/go.mod tools/security/go.sum
```

Formatting should print no files. Both tidy checks should leave their respective locks unchanged. In a sandbox, set writable `GOCACHE` and `GOMODCACHE` locations as needed. Run the pinned [security checks](security.md) before proposing production changes. Tests use temporary outputs and loopback HTTP servers; they do not run live qualification. Fixture tests must discover the complete synthetic fixture inventory.

## Layout and behavior contracts

`cmd/openshift-dc-converter/main.go` owns signals, process exit and linker metadata. `internal/migration/` contains command construction, the runner, conversion, planning, admission, cutover, recovery and reporting. Its tests share package internals. `migration.NewCommand(migration.BuildInfo{...})` passes explicit build information; linker targets remain `main.version`, `main.commit` and `main.buildDate`. Runner options, clients, clock, output and logging are injectable for tests.

`internal/migration/testdata/` holds synthetic fixtures. `internal/migration/compatibility/baseline-1.27.json` is the single embedded field catalog. `compatibility/` holds durable target policy and assessment conclusions. `tools/` retains directly invoked Python scripts; `tools/security/` is a separate Go module for scanners.

Preserve offline isolation, read-only generation/planning, paused zero-replica staging, ownership and freshness checks, exclusive stop-before-start ordering, journals, explicit recovery and failure propagation. Keep JSON-only protocol handling and single-attempt dynamic PUT requests separate from the guarded source-conflict retry loop. Validate raw specs before typed projection; preserve unfamiliar destination fields during recovery. Required-SCC authorization, ANP/BANP selection, quota and PV topology checks must remain before source retirement. Reports distinguish generation, staging, readiness, manual cases, failures and skips. Private atomic output and cancellation behavior are contract-tested.

## Candidates and source identity

```sh
python3 tools/build_candidates.py --output dist
```

Use a fresh output directory: old converter binaries are refused. The default targets are Linux amd64 and Darwin arm64. Repeat `--platform` to choose Linux amd64/arm64, Darwin amd64/arm64 or Windows amd64. These are build targets, not native execution claims. Output includes binaries, `SHA256SUMS` and `build-provenance.json`, explicitly marked unqualified. Provenance records the revision, dirty-tree status and current production/dependency/fixture identities. The candidate workflow also uploads security results.

Production identity includes every non-test Go file under `cmd/` and `internal/` plus the embedded catalog. Dependencies cover application `go.mod`/`go.sum`; the scanner module has a separate lock. Fixtures are hashed relative to their declared root. A code move, catalog, fixture or dependency change needs reassessment and exact-artifact qualification. Documentation changes alone do not change production identity. Never update frozen metadata to bless current binaries. [Historical reproduction and release checks](release-scope.md) are separate explicit operations.

## Regenerate the baseline catalog

Download the official [Kubernetes v1.27.16 OpenAPI document](https://raw.githubusercontent.com/kubernetes/kubernetes/v1.27.16/api/openapi-spec/swagger.json) to a private temporary file, then run:

```sh
python3 tools/extract_baseline.py /path/to/swagger.json
```

The generator deterministically writes `internal/migration/compatibility/baseline-1.27.json`, including upstream source identity. Review the diff and run baseline/conversion tests; do not substitute a newer schema because the client SDK changed.

## Tool inventory

Every Python command uses `python3 tools/<name>.py` from the root. Qualification drivers offer safe `--help`; their live entry points require explicit targets and mutation acknowledgement. The [qualification guide](qualification.md) describes inputs and additional fixture permissions.

| Tool / command | Consumer and retained purpose |
| --- | --- |
| `python3 tools/build_candidates.py --output dist` | Developers and candidate CI; current-source unqualified builds and provenance |
| `python3 tools/build_qualified.py --output frozen-dist --evidence-root /path/to/bundle` | Release maintainers; reproduce immutable historical binaries byte for byte |
| `python3 tools/release_gate.py --artifacts frozen-dist --evidence-root /path/to/bundle --historical-source` | Release maintainers/workflow; fail-closed exact-artifact and external-evidence verification |
| `python3 tools/check_vulnerabilities.py /path/to/govulncheck.json` | Security CI and maintainers; fail on symbol-level vulnerability findings or scanner errors |
| `python3 tools/extract_baseline.py /path/to/swagger.json` | Maintainers; deterministic embedded baseline regeneration |
| `tools/qualification_fixtures.py` | Imported only by local Python policy tests; creates temporary synthetic evidence, never release inputs |
| `tools/test_build_candidates.py`, `tools/test_build_qualified.py` | Unittest discovery above; candidate identity and historical reproduction regressions |
| `tools/test_release_gate.py` | Unittest discovery above; missing/tampered evidence, provenance, source and artifact rejection |
| `tools/test_qualification.py` | Unittest discovery above; local scenario-harness safety without cluster operations |
| `tools/security/go.mod`, `go.sum` | Security CI and maintainers; separately pinned scanner builds and checksums, commands in [security](security.md) |

Every retained `qualify_*.py` driver is listed with its command and purpose in the [live driver inventory](qualification.md#driver-inventory). Developers run these optional scenarios directly against an explicitly selected development cluster; GitHub workflows never run live qualification. Importing local test modules does not start a cluster operation. Keep raw logs, generated reports, credentials and review transcripts in ignored private output or an authorized external artifact store. Never commit them as documentation.
