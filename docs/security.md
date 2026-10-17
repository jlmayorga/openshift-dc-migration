# Security validation

Treat manifests, plans, snapshots and reports as private workload data: they can include environment values and admission diagnostics. Created files use mode 0600 and directories 0700; writes replace files atomically. Kubeconfig credentials are not intentionally copied into plans or reports. Restrict access to retained failure artifacts and qualification credentials.

## Pinned scanners

Application dependencies and scanner dependencies are separate. `tools/security/go.mod` and `go.sum` pin gosec, govulncheck and their transitive libraries, including gRPC 1.83.2. The gRPC fix addresses [GHSA-2v4p-qf9q-27wj](https://github.com/grpc/grpc-go/security/advisories/GHSA-2v4p-qf9q-27wj). The tooling module retains x/tools 0.52.0 for Go 1.27 export-data compatibility. Scanner updates do not themselves change the application's dependency hash or qualify a migration artifact.

Build scanners from the locked module and scan from the repository root:

```sh
SECURITY_DIR=$(mktemp -d)
go -C tools/security mod download
go -C tools/security mod verify
go -C tools/security build -mod=readonly -o "$SECURITY_DIR/govulncheck" golang.org/x/vuln/cmd/govulncheck
go -C tools/security build -mod=readonly -o "$SECURITY_DIR/gosec" github.com/securego/gosec/v2/cmd/gosec
"$SECURITY_DIR/govulncheck" -json ./... > "$SECURITY_DIR/govulncheck.json"
python3 tools/check_vulnerabilities.py "$SECURITY_DIR/govulncheck.json"
"$SECURITY_DIR/gosec" -fmt=json -out="$SECURITY_DIR/gosec.json" ./cmd/... ./internal/...
"$SECURITY_DIR/govulncheck" -mode=binary -json "$SECURITY_DIR/govulncheck" > "$SECURITY_DIR/scanner-vulnerabilities.json"
python3 tools/check_vulnerabilities.py "$SECURITY_DIR/scanner-vulnerabilities.json"
"$SECURITY_DIR/govulncheck" -mode=binary -json "$SECURITY_DIR/gosec" > "$SECURITY_DIR/gosec-vulnerabilities.json"
python3 tools/check_vulnerabilities.py "$SECURITY_DIR/gosec-vulnerabilities.json"
```

Check every command's exit status. Scanner failures fail validation and cannot count as clean scans. `check_vulnerabilities.py` enforces symbol-level findings from the full JSON event stream. The explicit gosec paths cover all shipped packages without traversing private scratch research or the scanner module. The ordinary Go package pattern for govulncheck remains `./...`.

Preserve complete scanner output in private storage or CI artifacts. Recheck advisories after dependency changes and before release; a past clean result is not a permanent security claim. Address findings through reviewed parent/transitive updates and rerun relevant behavioral tests. The explicit offline-input reader's G304 annotation records intended CLI file access; do not broaden it to cluster-derived paths or suppress findings to pass checks.

See [dependency policy](dependencies.md) for the application/client boundary and [release policy](release-scope.md) for exact-artifact qualification.
