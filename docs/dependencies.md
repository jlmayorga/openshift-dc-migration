# Dependency policy

Use Go **1.27.2**, as declared by both Go modules. Keep `k8s.io/api`, `k8s.io/apimachinery` and `k8s.io/client-go` aligned at the adopted **v0.37.1** set unless a separate reviewed upgrade changes them. The current application also retains Cobra 1.10.2, Testify 1.12.1 and sigs.k8s.io/yaml 1.6.0. The locks, rather than claims about the latest release, define reproducible inputs.

The adopted clients preserve the OpenShift 4.14 / Kubernetes 1.27 API baseline through explicit adaptations: JSON requests/responses regardless of ambient CBOR flags, rejection of CBOR replies, no mutation of global feature gates, baseline toleration semantics, raw-field validation and single-attempt dynamic updates. SDK support for a newer field does not authorize its use on the minimum target. The [completed historical source review](compatibility-review.md) records server/client assumptions and runtime prerequisites; current maintenance binaries require reassessment and new qualification.

## Changes and checks

Review a proposed dependency delta against the APIs, serialization, admission defaults and controller behavior the tool uses. Run [ordinary checks](development.md#local-checks), [security validation](security.md), fixture/baseline and HTTP contract tests. Application source, schema, fixture or dependency changes invalidate applicability of historical source-bound evidence. Passing local tests is separate from actual cluster qualification and native execution.

The scanner module under `tools/security/` has its own lock and security review. Its pinned gRPC 1.83.2 and x/tools requirements must remain separate from application requirements. Verify both module locks stay tidy; never move scanner dependencies into the application to simplify CI.

JSON is the canonical report. PDF is optional with `--report-path ''` to disable it. Retain `jung-kurt/gofpdf` for rendering compatibility; its upstream is archived, and `go-pdf/fpdf` is also archived. A separately scoped renderer replacement needs long-name, multipage, content and rendered-layout regressions. Report generation loads no external images, fonts or URLs.

CI pins Actions to commit SHAs and scanners through the separate module. `gofmt` and `go vet` are the Go style/static checks. Dependabot groups the Kubernetes modules and security transport libraries. Review grouped updates together; do not upgrade or relax checks merely to reproduce frozen historical bytes.

[Ordinary candidate builds](development.md#candidates-and-source-identity) describe the source actually built. [Historical reproduction](release-scope.md) uses immutable tested source and metadata. Neither cross-compilation nor a previous binary's result qualifies a changed artifact. Wider versions and platforms remain [follow-up work](backlog.md).
