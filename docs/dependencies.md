# Dependency policy

Use Go 1.27.2 for local validation, CI and release builds. Module-proxy metadata refreshed on 2026-10-15 identifies Cobra 1.10.2, Testify 1.12.1 and sigs.k8s.io/yaml 1.6.0 as current; these updates pass local race and request-contract tests.

Keep `k8s.io/api`, `k8s.io/apimachinery` and `k8s.io/client-go` aligned. The maintained 0.37.1 set is a qualification candidate, not an adopted baseline: the maintenance plan requires actual OpenShift 4.14 integration before adoption. The existing 0.31.3 set remains until that external gate passes. Neither set is currently qualified for release by this maintenance work. Dependency changes require fresh full-matrix integration evidence for the resulting binary.

JSON is forced for both requests and responses. The checked-in Kubernetes 1.27 baseline catalog prevents newer pod fields from silently entering ordinary conversion. These local checks do not prove old-server compatibility.

JSON is the canonical report. PDF is optional (`--report-path ''` disables it). Retain jung-kurt/gofpdf temporarily to preserve rendering; its upstream is archived. go-pdf/fpdf is also archived, so it is not an appropriate maintenance replacement. Any renderer replacement must pass long-name, multipage, content and rendered-layout regression checks before adoption. No external URL, image or font loading is used by report generation.

CI pins all Actions to resolved commit SHAs and security scanners to released versions. `go vet` and gofmt serve as the Go lint gates. Dependabot groups the three Kubernetes modules and the security transport libraries. No `dep`, legacy download shell pipeline, floating scanner branch, deprecated Actions output commands or archived release Action is used. Release jobs build with version, commit and deterministic commit-time metadata, attach SHA-256 checksums, and create only a draft after qualification. Publishing remains a maintainer action.

`tools/release_gate.py` requires actual OpenShift evidence per released minor and shipped binary SHA-256, tied to the exact revision, fixture tree and dependency file. Simulated, skipped, missing, stale or incomplete rows block release. The checked-in matrix deliberately records no actual integration passes until test-cluster results exist. Refresh the released-minor inventory before each release; it currently includes 4.14 through 4.22 and a development smoke row.
