# Security validation

On 2026-10-15, govulncheck 1.9.0 with Go 1.27.2 found eight advisory IDs with symbol-level paths before transitive upgrades:

GO-2026-4918, GO-2026-5026, GO-2026-5970, GO-2026-6603, GO-2026-6610, GO-2026-6611, GO-2026-6612, GO-2026-6617.

They affect x/net HTTP/2/IDNA and x/text normalization. Symbol reachability includes broad interface/formatting paths into HTTP/2 server code; this CLI does not start an HTTP/2 server. That observation does not waive the findings: all affected libraries were upgraded. x/net 0.61.0, x/text 0.43.0, x/sys 0.49.0 and its aligned x/term requirement replace vulnerable versions. The post-upgrade scan has no symbol-level advisory findings. Keep current scan artifacts; these are time-specific results, not a permanent security claim.

CI preserves the complete JSON event stream and enforces symbol-level findings with `tools/check-vulnerabilities.py`; scanner command errors also fail the job. Scanner failures are not treated as clean scans. Reevaluate advisories with parent dependency changes and before releases. Local before/after streams are retained in the ignored progress directory; CI uploads fresh streams rather than committing potentially large artifacts.

Input manifests, migration plans, backups and reports may include environment values. Files use mode 0600 and directories 0700 where created. Keep output private. Kubeconfig credentials are never intentionally included in plans or reports. Kubernetes errors can contain admission diagnostics and object values; protect reports accordingly.

The upstream gosec 2.29.0 dependency on x/tools 0.49.0 cannot decode Go 1.27 export data. `tools/security/go.mod` locks gosec and govulncheck with x/tools 0.52.0 and full checksums; CI builds these tools from that module and fails on scanner errors. The explicit-path offline reader's G304 annotation documents its intended CLI file access, not a suppression of cluster-derived file paths.
