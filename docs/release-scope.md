# Historical release policy

The completed historical scope covers exactly Linux amd64 and Darwin arm64 binaries on OpenShift 4.21.36 / Kubernetes v1.34.9. OpenShift 4.14–4.20 have completed source-review assumptions with [prerequisites](compatibility-review.md). OpenShift 4.22 and other platforms remain outside that scope. Current maintenance candidates require reassessment and new qualification.

The immutable [scope policy](../compatibility/release-scope.json) binds exact binary bytes, tested revision, production source, dependencies, fixtures and actual records. The [matrix](../compatibility/matrix.json) pins the review overlay and provenance chain. Do not update frozen hashes to match current source.

## Explicit historical operations

Provide a trusted external bundle root containing original `compatibility/evidence/` records and exact historical `compatibility/reviews/` snapshots. Missing or changed files fail closed. The bundle is optional for ordinary development. Do not substitute current durable assessment summaries for the pinned historical snapshots.

```sh
python3 tools/build_qualified.py --output frozen-dist --evidence-root /path/to/bundle
python3 tools/release_gate.py --artifacts frozen-dist --evidence-root /path/to/bundle --historical-source
```

Reproduction extracts the tested revision from local Git objects into temporary storage and builds with its frozen metadata. Fetch that revision if absent; never rebuild HEAD under historical labels. Verification with `--historical-source` also checks those immutable inputs. Omit that option to check current production identity; changed source is rejected by the old scope. `--full-matrix` remains the stricter wider check and requires additional actual evidence under the declared root.

Candidate builds, historical reproduction, eligibility verification and draft preparation are separate operations. Manual workflows consume selected artifacts and trusted evidence. Draft preparation uses an existing tag and never publishes; publication requires a separate maintainer action. Local verification does not establish that a GitHub release workflow ran.

## Workflows and artifact selection

The ordinary candidate workflow builds current source without evidence and uploads unqualified binaries, checksums, provenance and security results. Manual `reproduce.yaml` instead takes an `evidence_run_id` containing a trusted `qualification-bundle` artifact and uploads the reproduced historical candidates. Both use the artifact name `release-candidates`; select the originating run deliberately.

Manual `release.yaml` takes that trusted evidence run, the selected candidate run and an existing tag. It verifies frozen artifact bytes and archived source through `--historical-source`, exports validated evidence and prepares only a draft in the protected `release-review` environment. An ordinary changed-source candidate cannot pass the historical gate. No published binary installation method is implied by these workflows. Review and publication remain separate maintainer actions.

`--evidence DIRECTORY` selects replacement actual-record files from a directory **relative to `--evidence-root`**. It does not disable pinned scope or review checks. Paths and symlinks must stay inside the declared root. The default full-matrix record directory is `compatibility/results` inside that root. `--export-evidence DIRECTORY` writes only validated policy/records and gate results for private review. Never supply synthetic unit-test records as trusted evidence.
