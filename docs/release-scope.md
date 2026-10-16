# Completed maintenance release scope

On 2026-10-16 the owner narrowed the goal to the completed maintenance and available OpenShift 4.21 testing, and authorized opening the release gate. The active [release policy](../compatibility/release-scope.json) implements that decision. The maintenance goal is complete within this scope; broader qualification remains in the [follow-up backlog](backlog.md).

The scoped gate passes for these exact binaries on **OpenShift 4.21.36 / Kubernetes v1.34.9**. Each passed all 22 actual scenario groups, including new-app workloads, constraints before source retirement, migration/recovery and multiple namespaces. The [Linux report](linux-execution-tests.md) and [native/source-conflict report](source-conflict-live-tests.md) retain the detailed evidence and limitations.

| Platform | Binary SHA-256 |
| --- | --- |
| Linux amd64 | `cdbd5358de223a8120e5defdb415813e54250b0d368a3d02455cce4f5913e02c` |
| Darwin arm64 | `158b35236ef40ed00d5a4ad705a76979ab69aa55791089903e1bd4f3428f7d17` |

The minimum remains 4.14 / Kubernetes 1.27. OpenShift 4.14–4.20 retain their separately labelled source-review assumptions and prerequisites, with no actual older-cluster passes. OpenShift 4.22, Linux arm64, Darwin amd64, Windows amd64 and next-development smoke remain untested and outside this release. The matrix keeps its full inventory and `not-run` results; the active policy chooses the smaller release scope without relabelling the newest GA version.

## Reproduce and check the qualified candidates

From the repository root, with Go 1.27.2:

```sh
python3 tools/build_qualified.py --output dist
python3 tools/release_gate.py --artifacts dist --export-evidence evidence
python3 -m unittest discover -s tools -p 'test_*.py'
```

The builder emits only the two qualified platforms and verifies their bytes against the approved hashes. It preserves the tested version `maintenance-source-retry`, commit `eb8fb9d` and default build-date metadata, using `CGO_ENABLED=0`, `-mod=readonly`, `-trimpath` and `-buildvcs=false`. A new tag or documentation commit does not restamp the executable. Provenance records the tested source revision separately from the policy revision. Requiring the exact output hashes also prevents a host/toolchain difference from silently producing an unqualified release.

The gate validates the current production source, dependency and fixture hashes, pinned canonical evidence files, exact cluster/platform identity, all required scenarios and the existing hash-checked source assessments. Missing, changed or additional release binaries fail. Production changes or different linker metadata require new qualification and an explicit policy update. Documentation and release-policy commits can advance while the tested production bytes remain frozen.

`--export-evidence` copies the validated policy, matrix, actual records and review assessments alongside a machine-readable `gate-result.json`. `--full-matrix` retains the broader policy and currently fails because required 4.22 qualification is unavailable. It cannot be used to claim those tests passed.

The Build and Release workflow reproduces these candidates, runs security/quality checks and this scoped gate, then permits a **draft** release on an authorized tag. Live cluster requalification runs manually with the documented drivers; no self-hosted qualification workflow is configured because runners are unavailable. An executed GitHub workflow, push, tag or publication is not claimed by this local work; those remain maintainer actions.
