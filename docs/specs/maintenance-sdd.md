# OpenShift migration maintenance specification

Status: maintenance design implemented. The reconciled starting snapshot is repository revision `c60b7db`; its conditions are retained below as design context. Current binaries require source-review reassessment and actual qualification. The completed historical scoped result remains bound to its immutable source and exact artifacts. Execution notes, validation output and independent documentation reviews are kept in ignored private storage.

This specification turns the maintenance TODO into bounded work for AI coding agents using specification driven development (SDD). The intended result is a project that is easy to build, understand, maintain and use, with truthful documentation and optional development testing against disposable OpenShift clusters.

Execute waves in order. Keep only one wave active, and finish one task and its checks before starting the next task. Completing this specification does not require a running OpenShift cluster or imply that release qualification has passed.

## Source and current conditions

The source backlog is `.scratchpad/TODO.md`, reread on October 16, 2026. That directory is ignored by Git; the requirements below preserve its intent for agents working from a fresh checkout.

The repository currently has these conditions relevant to the work:

| Area | Current condition | Implication |
| --- | --- | --- |
| Go application | The command, runner, conversion, migration, constraints and reporting code share the root `main` package. | Separate the executable entry point from implementation without creating a package for every file. |
| Tests and baseline | Tests use root-relative `testdata` paths. `baseline.go` embeds `compatibility/baseline-1.27.json`. | Move fixtures and embedded assets deliberately; preserve their contents. |
| Dependencies | The application adopts aligned Kubernetes v0.37.1 clients and Go 1.27.2. The separate security-tooling module pins gRPC v1.83.2. | Preserve adopted versions and client adaptations; do not repeat or undo the completed upgrades. |
| Ordinary CI | `.github/workflows/ci.yaml` runs Go checks, Python unit tests and security checks without a cluster. Some Python policy tests use tracked canonical evidence. | Preserve cluster independence and make local unit tests independent of historical run files before removing those files. |
| Live qualification | `.github/workflows/compatibility.yaml` still runs on main pushes, a schedule and manual dispatch, using self-hosted runners. Its matrix now selects the active 4.21 release scope. | Keep scope selection while making live testing explicit and independent of normal checks. |
| Candidate and release workflow | `.github/workflows/build-and-release.yaml` invokes `tools/build_qualified.py`, which validates checked-in evidence before reproducing frozen binaries. The gate exports that evidence; it no longer downloads a qualification run. | Separate ordinary candidate builds from frozen-binary reproduction and release eligibility. The outstanding dependency is local evidence, not a mandatory fresh cluster run. |
| Active release scope | `compatibility/release-scope.json` covers the exact Linux amd64 and Darwin arm64 binaries qualified on OpenShift 4.21.36 / Kubernetes v1.34.9. | Preserve the completed scoped result. OpenShift 4.22 and other native platforms remain outside it; the full-matrix gate remains incomplete. |
| Evidence policy | `tools/release_gate.py` validates actual records, source-review assumptions, exact artifact sets, production source, dependencies, fixtures and assessment hashes. Loaders require particular repository paths. | Externalize raw evidence through a verified loader migration before deleting inputs. Keep actual and review-based claims distinct. |
| Runtime protections | New code includes `clients.go`, `raw_resources.go`, `scc.go`, `admin_policy.go` and source conflict handling, with dedicated regression tests. | Treat protocol, raw-field preservation, authorization, policy and retry behavior as explicit refactoring contracts. |
| Documentation | README and several documents mix instructions, capability statements, dated sessions and development history. | Distill operator and maintainer guidance; remove session narratives. |
| Existing backlog | `docs/backlog.md` marks the 4.14–4.20 source review, available 4.21 qualification and frozen-candidate validation complete. Wider version/platform testing and GitHub release execution remain follow-up work. | Preserve completed work and untested boundaries; do not reopen the review or require wider qualification to complete this maintenance plan. |

These observations are a starting point. Wave 0 must account for changes made after this specification was written. Preserve newer verified work, including a completed compatibility review; never restore an earlier status solely to match this snapshot.

### Decisions from the repository review

The six-wave sequence remains appropriate. No wave is marked complete merely because related maintenance landed elsewhere. Carry forward the adopted clients, completed source assessments, scoped qualification and security-tooling fix as baseline work.

Wave 1 must provide a build path that does not call the frozen-candidate verifier. Wave 2 must migrate evidence consumers and their tests before deleting tracked records. Wave 3 must replace the current root-only production-source hash inventory with explicit coverage of the new production paths. Moving code changes source identity and potentially binary bytes, so historical qualification cannot automatically transfer to the restructured executable. Local maintenance can finish while qualification of changed binaries remains pending.

## Requirements and traceability

| ID | Requirement derived from TODO | Owning wave |
| --- | --- | --- |
| R1 | README explains the project, prerequisites, installation or build, a useful first run, capabilities and limitations. | 5 |
| R2 | Detailed documents serve operators and maintainers without repeating README or exposing development notes. | 5 |
| R3 | A technical documentation agent writes the final docs; multiple persona agents review comprehension and usability. | 5 |
| R4 | Raw testing evidence and run information stay out of documentation and the tracked source tree. Documentation retains tested capabilities, results in summary and caveats. | 2, 5 |
| R5 | Go and Python code have a small, conventional layout with working builds, tests, imports and resource paths. | 3 |
| R6 | Every tool has a justified ongoing purpose or is removed as a development artifact. | 0, 2, 3 |
| R7 | Code is readable, direct and self-explanatory; unnecessary duplication and abstractions are removed without losing behavior. | 4 |
| R8 | Builds and ordinary CI work without OpenShift, cluster credentials or persistent cluster runners. Disposable clusters are optional development resources. | 1 |

## Scope and preserved behavior

This is maintenance work. Preserve the CLI command name, flags and defaults, migration semantics, plan/report formats, recovery protections and current compatibility claims unless a separately scoped bug fix is necessary. Build commands may change to the new command directory and must be updated wherever used.

The following contracts apply to every wave:

| ID | Contract | Required verification when affected |
| --- | --- | --- |
| C1 | Offline conversion does not load kubeconfig or contact a cluster. | Offline invocation with isolated HOME and invalid KUBECONFIG; conversion and report tests. |
| C2 | Generation and planning do not persist workload mutations. Staging creates only a paused, zero-replica destination. | Existing request-contract and staging tests. |
| C3 | Cutover retains ownership, stale-plan, admission and pre-retirement constraint checks. Exclusive migration stops source Pods before destination activation. | Constraint, storage, cutover and semantics suites. |
| C4 | Recovery retains journals, source snapshots and checks against foreign or orphan workloads. Cancellation and uncertain connectivity remain failures. | Recovery, cancellation and fault-path tests. |
| C5 | Report totals, nonzero failures, optional PDF output, private file modes and atomic writes retain their meaning. | Artifact, report, runtime and PDF tests as applicable. |
| C6 | Supported/unsupported feature classification, JSON API handling and embedded baseline validation remain equivalent. | Fixture, baseline, conversion and HTTP contract tests. |
| C7 | Synthetic tests, release-note assumptions and actual cluster observations remain distinguishable. Old evidence cannot qualify a changed artifact. | Release-gate negative tests and documentation review. |
| C8 | Only definite source-update conflicts may be retried, within the existing bound, after refetching and validating UID and the entire pinned spec. Cancellation interrupts backoff. Uncertain writes and SDK Retry-After responses are not replayed automatically. | `cutover_conflicts_test.go` and single-attempt client behavior in `clients.go`. |
| C9 | Raw admission and persisted workload fields are validated before typed projection. Unknown fields refuse automatic migration; explicit recovery preserves the raw representation. | `raw_resources_test.go`, admission and recovery tests. |
| C10 | Dynamic clients use JSON despite ambient CBOR flags, reject CBOR responses and do not mutate global feature-gate state. Baseline toleration semantics remain enforced with the adopted clients. | `client_contracts_test.go`, `baseline_values_test.go` and existing HTTP contracts. |
| C11 | Required-SCC use is verified for the workload service account; API-version-specific permission reviews, ANP/BANP selection checks and PV topology guards remain before source retirement. | SCC, admin-policy, access-review, scheduling and storage regression suites. |

Do not add migration features, upgrade dependencies or Go, replace the PDF library, expand the support matrix, perform the older-cluster compatibility review, provision a cluster, rewrite Git history or publish a release in these waves. Preserve existing unrelated edits and local scratch files. A defect discovered during maintenance gets a minimal separate fix with a reproducing test, rather than being hidden inside a file move.

Preserve qualification as a statement about its recorded source and exact binaries. When restructuring invalidates current source-review hashes or creates different binaries, mark those new candidates as requiring reassessment and qualification. Do not relabel the completed historical 4.21 release as untested, change its frozen metadata to match HEAD, or reuse its passing records for new output. The separate scanner-module update does not itself change the application's qualified dependency hash.

## Wave sequence and exit gates

| Wave | Outcome | Depends on | Exit gate |
| --- | --- | --- | --- |
| 0 | Baseline and explicit disposition of files and tools | None | Inventory, behavior baseline and planned paths are recorded. |
| 1 | Cluster independent CI and candidate builds | 0 | Default checks/builds need no cluster or historical evidence; scoped reproduction remains a separate operation. |
| 2 | Clean separation of source, tools and generated evidence | 1 | Evidence loaders and unit fixtures migrate before run files are removed; scope and assessment integrity remain enforced. |
| 3 | Minimal Go and Python layout | 2 | Moves compile and test; paths, embeds and provenance cover every production package; changed candidates remain unqualified. |
| 4 | Focused simplification of code | 3 | Small refactors preserve contracts and have clear reviewer explanations. |
| 5 | Formal documentation for users and maintainers | 4 | Examples work, claims match support summaries, persona review passes. |

Do not start a later wave merely because an earlier task is difficult. Split a large task into smaller changes within its wave. Optional live validation may be unavailable without preventing completion of local maintenance checks; record its actual status without counting it as passed.

## Wave 0 Establish the baseline

Owner: implementation agent acting as maintainer. Scope: inspection and planning; no runtime behavior changes.

### W0T1 Inventory files and consumers

Inventory tracked application code, tests, schemas, fixtures, Python tools, workflows and documentation. Assign every Python tool and each dated evidence document a disposition: keep, move, merge or remove. For retained tools, name the consumer and the behavior it provides. Include `build_qualified.py`, its tests, the added qualification drivers, release-scope policy, review consolidation records and assessment files. Check workflow references, imports, unit tests, regeneration requirements and direct maintainer commands before deciding a file is disposable.

Record the durable disposition table and final path map in an appendix to this specification. Put command output, temporary measurements and run notes in ignored scratch storage. Existing local files and edits are not cleanup candidates just because they are untracked.

Acceptance:

- Every tracked Python script, `tools/security` module, session document and evidence directory has a disposition and reason.
- The inventory distinguishes executable dependencies from optional development tools and generated output.
- All path consumers are identified, including `go:embed`, fixture lookups, Python imports, provenance hashes, linker flags and workflows.
- The inventory distinguishes ordinary builds, frozen reproduction, scoped release verification and full-matrix qualification. It maps `load_scope`, `load_reviews` and tests to every canonical evidence input they currently require.

### W0T2 Capture observable contracts

Build a baseline binary in temporary storage. Capture help/version output and run existing Go and Python checks. Use representative offline fixtures to capture manifests, plan/report structures, warnings and exit behavior. Normalize timestamps and other intentionally variable values when comparing later outputs. Record existing failures separately from regressions. Keep captured output outside tracked documentation.

Acceptance:

- Baseline checks have an explicit pass/fail result; any existing failures have a bounded explanation.
- C1 through C11 are mapped to relevant tests or a specific validation gap, including the newer client, raw-response, conflict, SCC and policy regressions.
- A future agent can compare the baseline and refactored executable without a cluster.
- Completed 4.14–4.20 review assumptions and the two-platform 4.21 scope are recorded separately from untested 4.22/platform follow-ups. A recorded local gate pass is not a claim that the GitHub release workflow ran.

### W0T3 Finalize the bounded plan

Confirm the proposed layout below against the inventory. Prefer the default; document a different choice only when it demonstrably reduces coupling or migration risk. Select the first implementation task and its exact write scope. Resolve only decisions needed for wave 1; avoid a broad architecture redesign.

## Wave 1 Separate CI from live qualification

Owner: workflow agent. Scope: `.github/workflows`, release-gate tests and narrowly necessary helper changes. Keep current application and tool paths during this wave.

### W1T1 Make live testing an explicit operation

Remove automatic push and scheduled cluster qualification. Keep an explicit manual workflow for an available disposable cluster, requiring a selected candidate and target configuration. Do not require permanently installed self-hosted runners. Use a hosted runner capable of reaching the selected cluster, or make an explicitly selected temporary runner an optional execution choice.

Match each runner's operating system and architecture to the native execution claim. A Linux hosted runner cannot qualify a Darwin executable by cross-building it. Select an appropriate native hosted or temporary runner for each requested platform, or retain its untested status when none is available.

The manual operation must validate inputs before mutations, use its declared context and test namespace, preserve test mutation acknowledgements, and fail clearly on missing configuration or unreachable targets. An absent cluster does not trigger the workflow. A dispatched but unsuccessful qualification cannot become a passing test result.

Acceptance:

- Push and pull-request checks have no dependency on live qualification, `oc`, cluster secrets or cluster runners.
- Live qualification is dispatched explicitly and cannot select the operator's current kubeconfig implicitly.
- The workflow has no permanent runner assumption and retains restricted access to test credentials.

### W1T2 Separate candidates from release eligibility

Ordinary candidate builds must complete and upload binaries, checksums, security results and current-source provenance with no qualification evidence configured. They must not invoke `build_qualified.py` or claim to reproduce the old qualified binaries. Keep frozen-candidate reproduction as a separate explicit operation with its existing byte checks and tested metadata. Move release verification and draft preparation into a separate operation that consumes selected candidates and verified evidence.

Preserve the implemented active scope: source-review assumptions for 4.14–4.20 and exact actual qualification for the two selected 4.21 binaries. Do not restore an all-versions live-test requirement or expand the scoped platform set. Preserve `--full-matrix` as a separate stricter check with incomplete targets. Missing, simulated, mismatched, stale or incomplete evidence must prevent a qualified draft; ordinary candidate builds still succeed. Publishing remains a maintainer action.

Acceptance:

- A candidate-only run succeeds without cluster configuration, canonical run evidence or a frozen-scope match; its metadata identifies the source actually built.
- Frozen reproduction retains its tested metadata and rejects different bytes. Tested-source revision and current policy revision remain distinct.
- The release operation checks the exact artifact set, native execution platforms, source, fixture, dependency and assessment hashes against trusted evidence.
- Existing release-gate negative cases still fail. No evidence is synthesized from fixture tests.
- Draft creation remains separate from publication.

### W1T3 Verify the workflow boundary

Validate workflow syntax and inspect all trigger and `needs` relationships. Exercise candidate commands locally where possible and add focused tests for changed release orchestration. Exercise rejection of missing evidence without contacting OpenShift. Include the minimal explanation of the new commands in the existing maintainer guide; the full rewrite belongs to wave 5.

Exit: default validation and candidate production work in an environment without `oc`, kubeconfig, OpenShift secrets or self-hosted cluster runners. Package downloads and security databases may still require ordinary network access; cluster independence does not mean air-gapped operation.

## Wave 2 Classify tools and remove run artifacts

Owner: tooling agent. Scope: Python helpers, evidence storage, compatibility summaries, ignore rules and affected references. Do not reorganize Go packages yet.

### W2T1 Retain only reusable tooling

Use the wave 0 inventory to make explicit decisions. The initial candidates for retention are:

| Existing files | Ongoing purpose to verify |
| --- | --- |
| `check-vulnerabilities.py`, `tools/security` | CI security policy and reproducible scanner builds |
| `release_gate.py`, `test_release_gate.py` | Integrity checks on external release evidence |
| `build_qualified.py`, `test_build_qualified.py` | Explicit reproduction of the historical scoped binaries, separate from ordinary builds |
| `extract-baseline.py` | Deterministic regeneration of the embedded schema |
| `qualify_*.py`, `test_qualification.py` | Reusable optional live testing and local harness tests, including admin-policy, admission, raw-field and source-conflict drivers |

Do not delete a live driver solely because ordinary CI will no longer run a cluster. Remove a driver only if it is superseded or has no retained purpose; preserve its useful local tests and scenarios through the replacement. Keep provenance helpers usable without importing or invoking live tests. Avoid adding a Python packaging dependency merely to organize these scripts.

Acceptance: each retained tool has a documented command and consumer, removed files have no remaining consumers, and importing test modules does not start cluster work.

### W2T2 Separate evidence from source control

First migrate evidence consumers. `load_scope` currently reads and hashes canonical files even with an evidence override, `load_reviews` validates repository-relative paths and assessment hashes, and `build_qualified.py` calls both through scope verification. Merely passing `--evidence` or deleting the directory will break those operations and policy tests.

Introduce an explicit external evidence root or bundle for actual records and source-bound review overlays. Resolve references within that declared root, validate identities and pinned hashes, and fail closed on missing or changed inputs. Keep release policy, official source references and durable assessment conclusions tracked; move run snapshots and extraction dumps out. Preserve the reviewed assumption's provenance and resolution overlay rather than replacing it with an unchecked summary. Retain exact bytes of externally migrated hash-pinned records.

Make release-gate and builder unit tests use small synthetic records in temporary directories. They must not depend on production run files, external credentials, a cluster or access to a historical artifact store. Test rejection of missing evidence, changed assessment content, stale source, wrong platform and extra artifacts. Synthetic passing unit fixtures must never be exported as actual release evidence. Complete the loader and test migration before removing its old inputs.

Then distill dated live-test documents and `compatibility/evidence` into a concise version/capability summary containing evidence basis, observed coverage, pass/partial/unsupported status and limitations. Preserve the completed historical scoped qualification and source-review conclusions without transferring them to changed candidates.

Remove raw session JSON and dated session narratives from the tracked tree after extracting useful conclusions. Existing records may be retained privately in ignored output or an already authorized external artifact store. Do not rewrite Git history or require a new storage service. Replace matrix links to deleted files with the durable summary or appropriate external references. Keep source schemas, synthetic fixtures, deterministic generated baseline data and release policy tracked.

Raw release evidence remains external to Git. Its records still carry exact artifact and test provenance for verification; public documentation contains only useful summaries. If evidence is missing or expired, exact-artifact qualification is unavailable and the release verifier fails. Frozen reproduction may also refuse without its verification inputs; ordinary tests and candidate builds remain usable. Do not relax the verifier to keep historical commands green.

Update `docs/backlog.md` where its proposed evidence destination conflicts with this policy. Preserve its compatibility-review scope and current verified completion status; structured assessments should contain durable conclusions and source references, not session dumps.

Acceptance:

- No tracked logs, run archives, credentials, generated conversion output, per-session evidence or dated test narratives remain in the maintained tree.
- Ignore rules cover agreed generated locations without hiding schemas, fixtures or source.
- Matrix and documentation references resolve; the matrix no longer embeds session transcripts, case counts or local archive locations.
- External evidence with wrong hashes or provenance is still rejected by focused tests.
- Completed historical scoped qualification and review assumptions are retained with their prerequisites. Partial or untested targets remain distinct from those completed results.
- Ordinary Python unit tests pass after canonical run evidence is absent; explicit historical reproduction and release checks require their verified external inputs.

### W2T3 Verify a clean checkout

Run local Go/Python checks and build candidates using only tracked files plus normal dependency downloads. Verify external evidence is consumed only by explicitly requested qualification/release operations. Patch links and instructions affected by artifact removal now, so intermediate commits remain usable.

## Wave 3 Introduce a minimal project layout

Owner: structure agent. Scope: mechanical moves, package boundaries, Python organization and all path consumers. Keep behavioral simplification for wave 4.

### W3T1 Move Go code and its assets

Use this default layout. Start with one cohesive implementation package because many existing operations are methods on `Runner` and the tests share package internals.

```text
cmd/openshift-dc-converter/main.go     executable, signals, exit status, build metadata
internal/migration/                  command construction, runner and migration implementation
internal/migration/*_test.go         existing unit and contract tests
internal/migration/testdata/         synthetic conversion fixtures
internal/migration/compatibility/    embedded baseline schema
compatibility/                      durable compatibility policy and summaries
tools/                              maintained developer tooling
docs/                               user and maintainer documentation
docs/specs/                         development specifications
```

Move command construction into `internal/migration/command.go`; keep the executable entry point thin. Pass build metadata from `main` into command construction through a small explicit value so `--version` remains accurate. Keep the existing runner/test injection model. Do not introduce a public `pkg` API, dependency injection framework, interface per type or many mutually dependent packages. Extract an additional package only when the inventory establishes a concrete independent responsibility.

Move baseline JSON inside its embedding package and update the generator's destination. A Go embed cannot reference `../..`. Keep one canonical schema copy. Move fixtures with their tests and update matrix references and provenance producers/consumers together. Compute fixture digests relative to the declared fixture root consistently; a path migration must never make old evidence appear valid for a new binary.

Replace `production_source_digest()`'s root `*.go` scan with a documented inventory covering all shipped Go files under `cmd` and `internal`, plus the embedded catalog. Exclude test code and scanner-tool dependencies consistently. Update every producer and verifier of that digest. Test that changing a nested production file or embedded schema invalidates the binding, while a documentation-only edit does not. Never retain the root-only glob after moving application code out of it.

Preserve the old scope as historical policy rather than rewriting its hashes to bless the moved executable. Current candidate provenance must record the new layout and bytes; current source-review applicability requires reassessment, and actual qualification requires new evidence. If historical reproduction is retained, use its immutable tested source with matching verified inputs in an isolated checkout. It must not rebuild current code under old labels. Missing live resources may leave new release eligibility pending while the local maintenance waves complete.

Acceptance:

- `go build ./cmd/openshift-dc-converter` works and the root contains no application implementation files.
- Help/version behavior, flags, default paths and output schemas retain their contracts.
- All local tests still execute; fixture discovery cannot silently succeed with zero fixtures.
- Embedded schema contents and fixture contents are unchanged by the move.
- CI formatting checks cover nested Go directories; build paths and `-ldflags` metadata still work.
- Test, generator, matrix and qualification paths point to the same canonical fixture/schema roots.
- All new production packages participate in source hashing; tests detect nested-source drift and reject an old scope for changed candidates.
- Normal builds and tests pass without reproducing the historical qualified hashes. No new binary inherits a historical live pass.

### W3T2 Organize retained Python tools

Keep the scripts layout if it remains clear. If the inventory justifies grouping, use a small structure such as `tools/ci`, `tools/schema`, `tools/qualification` and colocated tests. Record exact destination paths before moving. Use snake_case for Python module names. Keep the pinned scanner Go module identifiable under `tools/security` unless a documented consumer requires a move.

Make supported invocations independent of accidental import search paths. Choose script invocation or module invocation consistently; add only the minimal package files or entry points needed. Resolve repository assets from an explicit repository root or `__file__` where appropriate. Avoid scattered `sys.path` modifications. Keep live operations behind explicit entry points and existing target/mutation validation.

Acceptance: retained commands and test discovery work from the documented directory, imports have no cluster side effects, and workflows no longer depend on obsolete tool paths. Add behavior tests only for changed path resolution or entry points.

### W3T3 Update all consumers and compare behavior

Update workflows, security scanner scopes, dependency automation if affected, linker settings, build commands, schema regeneration, fixture hashes and maintainer instructions in the same change as each move. Run the baseline comparison from wave 0 and the full local gate. Do not combine dependency upgrades, new policies or code cleanup with moves.

## Wave 4 Simplify implementation without changing behavior

Owner: refactoring agent. Scope: one concern per change within the established layout.

### W4T1 Select bounded refactors

Review application flow, planning, cutover/recovery, constraints, quota/scheduling, conversion/reporting and Python harnesses for concrete readability problems. Include the new client adapters, raw-resource handling, SCC authorization and admin-policy checks. Candidates include repeated parsing, repeated validation, deeply nested control flow, unclear names, oversized functions, obsolete compatibility shims and comments that restate code.

Record each selected refactor with its files, the comprehension problem, protected contracts and tests. Select only refactors with a clear payoff. A long function or defensive check is not automatically unnecessary. Ownership checks, journal writes and mutation ordering must remain visible to a reviewer.

### W4T2 Refactor one concern at a time

Use descriptive names and direct control flow. Extract helpers around a real repeated operation, with explicit inputs and errors. Keep state transitions and side effects understandable. Share Python harness utilities only where they represent stable behavior; avoid a general testing framework.

Preserve failure propagation and cancellation. Do not replace defensive comparisons with weaker checks, collapse distinct error outcomes, remove recovery states, or merge dry-run and persistent writes to reduce line count. Preserve error meaning and actionable diagnostics.

Keep single-attempt SDK PUT behavior separate from the guarded source-conflict loop. Preserve full-spec and identity checks on each conflict retry, cancellation-aware backoff, JSON-only negotiation and raw-object recovery. Similar-looking typed and unstructured code paths may enforce different contracts; establish equivalence before merging them.

Acceptance for each change:

- The reviewer can explain the simplified flow and why each affected contract is preserved.
- Relevant existing tests pass; new tests reproduce a meaningful uncovered behavior or defect.
- Baseline outputs are equivalent after normalization, except any explicitly isolated defect fix.
- No unexplained public API, format, dependency or feature changes are included.

### W4T3 Verify the integrated result

Run the full local gate once after the selected refactors. Review the aggregate diff for unrelated changes and hidden policy changes. Optional live testing, when explicitly requested against an available authorized cluster, may add confidence; absent live results cannot be inferred from local tests.

## Wave 5 Write formal documentation and review usability

Owner: technical documentation agent. Reviewers: at least three distinct persona agents. Begin after the executable layout and behavior stabilize. Earlier waves must still repair any links or commands they break.

### W5T1 Write the README and focused guides

README should answer, in order: what the tool does, who should use it, prerequisites and build/install, one successful offline example, the live workflow overview, capabilities/limitations and links to deeper guidance. Put the first useful command near the beginning. Document installation methods that actually exist; do not invent a published binary release.

Use focused guides for migration/recovery, feature and compatibility coverage, mode-specific RBAC, development checks, optional live testing, dependency/security policy and release preparation. Merge existing pages where they repeat the same guidance. Link each concept to one authoritative location.

Describe tested behavior and caveats in plain language. Clearly distinguish the completed historical 4.21 scoped release, 4.14–4.20 source-review assumptions, wider untested targets, and pending qualification of any newly restructured binaries. Preserve limits of preflight and platform/storage coverage. Remove development chronology, retry stories, run IDs, test namespace names, source/destination UIDs, raw hashes, transcript blocks and session case counts. Durable compatibility conclusions, prerequisites and official technical references are useful; session details belong in external evidence. Security guidance should describe the process and current requirements rather than narrating one scanner run. It must retain the separation between application and scanner dependencies.

Acceptance:

- A new user can build and convert a supplied synthetic fixture without a cluster.
- README distinguishes generation, staging, cutover and rollback and explains where disruptive operations occur.
- Unsupported cases, privilege requirements, compatibility evidence basis and current limitations are honest and easy to find.
- A maintainer can run normal checks, regenerate the schema, understand retained tools and explicitly request live tests without reading development history.
- All documented commands use final paths; internal links resolve; capability statements agree with code and durable summaries.

### W5T2 Review through independent personas

The writing agent requests separate reviews from:

| Persona | Review task | Passing result |
| --- | --- | --- |
| First-time user | Follow README from a fresh checkout through offline conversion. | No missing prerequisites, undocumented paths or unexplained output. |
| OpenShift operator | Read planning, staging, cutover, RBAC and recovery instructions. | Understands required access, state changes, refusal conditions and recovery boundaries. |
| Maintainer | Follow build/test/tooling/release instructions without a cluster. | Normal checks are sufficient for development; optional qualification and release claims are clear. |

Each reviewer reports specific confusing or incorrect passages and proposed corrections. The author resolves findings, then affected reviewers recheck. Operator review does not require executing a live migration. Store review transcripts and command output outside tracked documentation. Multi-agent review is an explicit requirement of this wave; do not start unrelated implementation work in parallel.

### W5T3 Complete the documentation gate

Run documented offline commands in temporary directories and review the final outline, links and support claims. Keep the README concise enough to start using the tool without reading every guide. Complete all three persona reviews with no unresolved correctness or onboarding blockers.

## Validation commands and evidence

Before restructuring, use the existing entry points:

```sh
go mod download
go mod verify
go build -mod=readonly -trimpath -o /tmp/openshift-dc-converter .
go test -mod=readonly -race ./...
go vet -mod=readonly ./...
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s tools -p 'test_*.py'
```

After wave 3, build `./cmd/openshift-dc-converter` instead of `.`. Update Python discovery to the final agreed layout and ensure it discovers the retained suites. Use the repository's declared Go toolchain and pinned security tool module. Perform formatting, module cleanliness, security and workflow checks required by CI when those areas change. A temporary HOME for isolation must not overwrite the operator's environment or credentials.

`tools/build_qualified.py` and `tools/release_gate.py --artifacts ...` are currently historical scoped reproduction/verification operations, not the normal build gate for new code. Wave 1 separates their orchestration, wave 2 migrates their evidence inputs, and wave 3 accounts for changed source identity. Full-matrix or new-binary release verification can legitimately remain blocked on missing actual qualification; ordinary validation must not depend on that result.

Run checks proportional to each task: workflow validation for workflow edits, local Python tests for tooling, existing targeted suites for behavior changes, and the full local gate after moves and integrated refactoring. Do not rerun the full suite for prose corrections. Live qualification never runs as a side effect of a local check.

Store test results, baseline captures, agent handoffs and review logs in ignored private scratch storage or an authorized artifact destination. Report the commands and outcomes to the user; do not commit the run output. This specification and durable architecture/tool decisions are maintained design material, not execution logs.

## Agent execution contract

Start an implementation session by selecting one task ID and rereading its requirements and predecessor gate. Inspect applicable repository instructions and current working-tree changes. Use the installed codebase-memory skill for structural exploration, verify index coverage for relied-on files, and read source for gaps or changes.

Before editing, identify the files owned by the task and the contracts it can affect. Preserve other contributors' edits. Keep the task within the active wave. If a discovery requires changing scope, update the requirement/design before implementing it; document unrelated opportunities for a later wave.

At task completion, provide changed paths, the concrete outcome, checks and their results, unresolved limitations, and the next eligible task. A skipped test is not a pass. Do not mark a wave complete until its acceptance criteria and exit gate are satisfied. Keep transient completion tracking in an ignored work log.

Use this prompt to hand a task to an AI coding agent:

```text
Implement task <WnTn> from docs/specs/maintenance-sdd.md.
Active wave: <n>. Predecessor gate: <completed result>.
Write scope: <specific files or modules>.
Preserve contracts <C IDs> and all unrelated working-tree edits.
Read current instructions and source before editing. You are sharing the
codebase with other contributors; accommodate their edits and do not revert them.
Run the task's acceptance checks. Keep raw outputs in ignored storage.
Finish with changes, validation results, remaining limits and next eligible task.
Stop after this task; do not start another wave or publish a release.
```

## Completion criteria

All eight requirements have completed owning waves. A clean checkout builds candidates and passes ordinary checks without OpenShift. Source, fixtures, schemas, tools and generated output have clear homes. Refactoring preserves observable migration behavior. Final documentation supports a first run and ongoing maintenance, passes persona review and contains useful capability summaries without session narratives. Qualification and publication retain their separate, truthful gates.

## Appendix A: reconciled disposition and path decisions

The inspected baseline is `c60b7db`. Adopted Go/Kubernetes clients, the scanner module, runtime safeguards, completed source assessments and historical two-platform qualification are preserved. The existing `.gitignore` change and this specification are pre-existing local work. No newer implementation supersedes these waves.

| Source | Disposition / final path | Consumer and ongoing purpose |
| --- | --- | --- |
| `tools/build_qualified.py` | Keep | explicit historical binary reproduction; release maintainers |
| `tools/check-vulnerabilities.py` | Move to `tools/check_vulnerabilities.py` | scanner-result policy; CI security jobs |
| `tools/extract-baseline.py` | Move to `tools/extract_baseline.py` | deterministic embedded field catalog regeneration; maintainers |
| `tools/qualify_admin_policy.py` | Keep | optional explicit disposable-cluster scenario driver; qualification maintainers and local harness tests |
| `tools/qualify_admission.py` | Keep | optional explicit disposable-cluster scenario driver; qualification maintainers and local harness tests |
| `tools/qualify_configurations.py` | Keep | optional explicit disposable-cluster scenario driver; qualification maintainers and local harness tests |
| `tools/qualify_conflicts.py` | Keep | optional explicit disposable-cluster scenario driver; qualification maintainers and local harness tests |
| `tools/qualify_constraints.py` | Keep | optional explicit disposable-cluster scenario driver; qualification maintainers and local harness tests |
| `tools/qualify_features.py` | Keep | optional explicit disposable-cluster scenario driver; qualification maintainers and local harness tests |
| `tools/qualify_namespaces.py` | Keep | optional explicit disposable-cluster scenario driver; qualification maintainers and local harness tests |
| `tools/qualify_new_app.py` | Keep | optional explicit disposable-cluster scenario driver; qualification maintainers and local harness tests |
| `tools/qualify_openshift.py` | Keep | optional explicit disposable-cluster scenario driver; qualification maintainers and local harness tests |
| `tools/qualify_permissions.py` | Keep | optional explicit disposable-cluster scenario driver; qualification maintainers and local harness tests |
| `tools/qualify_raw_fields.py` | Keep | optional explicit disposable-cluster scenario driver; qualification maintainers and local harness tests |
| `tools/qualify_recovery.py` | Keep | optional explicit disposable-cluster scenario driver; qualification maintainers and local harness tests |
| `tools/qualify_storage.py` | Keep | optional explicit disposable-cluster scenario driver; qualification maintainers and local harness tests |
| `tools/qualify_worker.py` | Keep | optional explicit disposable-cluster scenario driver; qualification maintainers and local harness tests |
| `tools/release_gate.py` | Keep | fail-closed evidence and exact-artifact verification; release workflow |
| `tools/test_build_qualified.py` | Keep | synthetic frozen-builder regression tests; ordinary CI |
| `tools/test_qualification.py` | Keep | local harness safety tests; ordinary CI |
| `tools/test_release_gate.py` | Keep | synthetic release-policy regression tests; ordinary CI |
| `tools/security/go.mod`, `go.sum` | Keep | Pinned scanner builds, separate from application dependencies |
| `docs/admin-policy-live-tests.md` | Merge conclusions into `docs/compatibility.md`; remove narrative | Operators need observed capabilities and limits, not session transcripts |
| `docs/admission-scc-live-tests.md` | Merge conclusions into `docs/compatibility.md`; remove narrative | Operators need observed capabilities and limits, not session transcripts |
| `docs/client-upgrade-live-tests.md` | Merge conclusions into `docs/compatibility.md`; remove narrative | Operators need observed capabilities and limits, not session transcripts |
| `docs/linux-execution-tests.md` | Merge conclusions into `docs/compatibility.md`; remove narrative | Operators need observed capabilities and limits, not session transcripts |
| `docs/live-tests-2026-10-15.md` | Merge conclusions into `docs/compatibility.md`; remove narrative | Operators need observed capabilities and limits, not session transcripts |
| `docs/multiple-namespaces-live-tests.md` | Merge conclusions into `docs/compatibility.md`; remove narrative | Operators need observed capabilities and limits, not session transcripts |
| `docs/new-app-live-tests.md` | Merge conclusions into `docs/compatibility.md`; remove narrative | Operators need observed capabilities and limits, not session transcripts |
| `docs/preflight-live-tests.md` | Merge conclusions into `docs/compatibility.md`; remove narrative | Operators need observed capabilities and limits, not session transcripts |
| `docs/pv-topology-live-tests.md` | Merge conclusions into `docs/compatibility.md`; remove narrative | Operators need observed capabilities and limits, not session transcripts |
| `docs/raw-response-live-tests.md` | Merge conclusions into `docs/compatibility.md`; remove narrative | Operators need observed capabilities and limits, not session transcripts |
| `docs/recovery-live-tests.md` | Merge conclusions into `docs/compatibility.md`; remove narrative | Operators need observed capabilities and limits, not session transcripts |
| `docs/source-conflict-live-tests.md` | Merge conclusions into `docs/compatibility.md`; remove narrative | Operators need observed capabilities and limits, not session transcripts |
| `docs/storage-guard-live-tests.md` | Merge conclusions into `docs/compatibility.md`; remove narrative | Operators need observed capabilities and limits, not session transcripts |
| `docs/worker-expiry-live-tests.md` | Merge conclusions into `docs/compatibility.md`; remove narrative | Operators need observed capabilities and limits, not session transcripts |
| `compatibility/evidence/` (all records) | Move exact bytes to an external evidence root | Scope artifact evidence, source-review consolidation and resolution overlays; explicit release verification only |
| `compatibility/reviews/*-candidates.json` | Move to external evidence root | Extraction dumps; preserve privately for review provenance |
| `compatibility/reviews/*-assessment.json` | Keep durable conclusions and official references; preserve exact historical snapshots externally | Operators read conclusions; pinned historical review overlays verify external snapshots |
| Root production Go files | Move to `internal/migration/` | One cohesive implementation package; preserve runner injection and private tests |
| `main.go` | Split `cmd/openshift-dc-converter/main.go` and `internal/migration/command.go` | Signals/exit/linker metadata stay in main; command accepts explicit metadata |
| Root `*_test.go`, `testdata/` | Move with implementation | Contract tests and synthetic fixture suite, never actual evidence |
| `compatibility/baseline-1.27.json` | Move to `internal/migration/compatibility/` | One canonical embedded schema, unchanged bytes |
| `compatibility/matrix.json`, `release-scope.json` | Keep | Durable target inventory and immutable historical scope policy |
| Other guides, README | Keep/merge by subject in wave 5 | One authoritative operator/maintainer guide per subject |
| `.github/workflows/ci.yaml` | Keep, repair nested paths | Cluster-independent tests, formatting, module/security checks |
| `build-and-release.yaml` | Candidate build only; separate manual reproduction and release workflows | Candidate production independent of qualification; drafts require verified exact inputs |
| `compatibility.yaml` | Manual native-platform qualification | Explicit candidate/context/namespace and mutation acknowledgment; hosted runner |

The flat `tools/` script layout is retained: drivers share `Live`/`NewApp` helpers and release digest functions. Snake-case script names are used consistently. No packaging dependency is introduced. Commands are `python3 tools/<name>.py`; unittest discovery remains `python3 -m unittest discover -s tools -p 'test_*.py'`.

Path consumers: `baseline.go` embeds the catalog; `fixture_matrix_test.go` reads matrix fixture paths; conversion tests use package-relative `testdata`; `extract_baseline.py` writes the catalog. Qualification drivers compute fixture/dependency digests and `qualify_openshift.py` computes production identity. `release_gate.py` is the canonical digest producer/verifier. Workflow builds and documented Go commands move from `.` to `./cmd/openshift-dc-converter`; linker targets remain `main.version`, `main.commit`, `main.buildDate`. Recursive formatting/security scopes include `cmd` and `internal`.

Canonical evidence dependency map: `load_scope` reads `compatibility/release-scope.json` and hash-checks its Linux-current-full and Darwin source-conflicts records. `load_reviews` reads the matrix's source-retry review, then hash-checks every assessment referenced by that overlay. `verify_scope` calls `load_reviews`; both builder tests and scoped gate tests currently consume this chain. All of these consumers must accept a declared evidence root before canonical records are removed. Source-bound overlays and historical assessment bytes resolve inside that root; durable current assessments remain tracked separately. Ordinary candidates do not invoke this chain. Frozen reproduction uses immutable tested source in isolation and verifies output bytes; scoped release requires those exact bytes plus verified evidence; full-matrix verification remains a distinct stricter incomplete operation.

Production hashing will inventory every non-test Go file under `cmd/` and `internal/` and the canonical embedded catalog. Historical root-layout hashing is used only for explicitly selected immutable historical source. Fixture hashing is relative to its declared root. Neither path migration nor current documentation can grant historical qualification to new candidates.

Additional retained tools: `tools/build_candidates.py` produces unqualified current-source candidates for ordinary CI; `tools/dispatch_qualification.py` validates explicit native hosted qualification configuration and installs a checksum-pinned oc client. Their colocated tests are ordinary synthetic tests. Every retained driver remains an explicit script with a guarded main entry point.

External evidence design: `--evidence-root` declares a bundle root containing original `compatibility/evidence/` records and exact historical `compatibility/reviews/` snapshots. References must resolve inside that root, including through symlinks. Matrix policy pins the source-review overlay hash; scope policy retains original actual-record hashes. Current durable assessment summaries never replace historical hash-pinned snapshots. Historical reproduction extracts the immutable tested Git revision into temporary storage, checks its production/dependency/fixture digests, and rebuilds there with frozen linker metadata. Current policy and tested revision stay distinct.

Security source scope is explicit after the move: gosec scans `./cmd/... ./internal/...`, covering every shipped Go package while leaving ignored user scratch/research and the separately pinned scanner module outside application scans. No source rule or severity threshold is suppressed.

## Appendix B: bounded readability decisions

The flow review covered command/runner dispatch, planning and journals, exclusive/overlap cutover and recovery, constraints, quota/scheduling, conversion/reports, JSON/single-attempt clients, raw-field projection, SCC authorization and admin-policy selection. State ordering and the distinct typed/raw and mutation/dry-run paths remain explicit; similarity alone does not establish equivalence.

| Selected change | Comprehension problem and payoff | Protected contracts and checks |
| --- | --- | --- |
| Replace bespoke string membership loop with `slices.Contains` in converter, options/namespace checks and stored-plan validation | Removes a redundant utility in favor of the standard operation; nil/empty slices and string equality retain identical meaning | C1/C3/C6; existing option, conversion, fixture and stored-plan tests plus normalized outputs |
| Keep an explicit current record while validating the review provenance chain | Avoids modifying the top overlay merely to carry a base hash, then reparsing it; all hash, cycle and containment guards remain visible | C7; synthetic base-chain tampering/cycle tests and external historical bundle verification |
| Format retained Python tools with the installed formatter | Dense new build/fixture/orchestration expressions obscure branch boundaries; formatting changes no behavior | Existing local Python suites and invocation checks |

No dependency, public format, migration policy or safety-gate change is part of these refactors. Complex scheduling/admission/recovery checks have concrete distinct responsibilities and remain intact.

`tools/qualification_fixtures.py` is retained test support, imported only by release-policy and frozen-builder unit tests. Its temporary synthetic records are never a qualification export or production evidence input. The colocated `test_build_candidates.py` and `test_dispatch_qualification.py` cover the added candidate/workflow boundaries through normal unittest discovery.

Release integrity applies to every evidence selection path: replacement actual-record directories receive the same containment and immutable byte-hash checks as canonical scoped records. Full-matrix actual observations must bind the production source and native artifact platform as well as revision, fixtures and dependencies. Qualification dispatch compares selected candidate provenance with the checked-out source identities before installing the client or accessing the cluster. These checks retain the existing scope and do not convert unqualified inputs into qualified output.
