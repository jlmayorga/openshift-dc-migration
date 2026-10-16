# Compatibility and observed coverage

Compatibility claims have three different bases. Synthetic fixtures and HTTP contract tests check local behavior. Official-source assessments and the bounded maintenance delta refresh support conditional source assumptions. Actual observations qualify only their recorded source and exact binary bytes.

| Target | Basis and result | Limits |
| --- | --- | --- |
| OpenShift 4.14–4.20 | Completed official-source review and maintenance delta refresh: assumed compatible with prerequisites | No actual older-cluster integration. Applies only to the reviewed source/dependency/catalog/fixture identities. |
| OpenShift 4.21.36 / Kubernetes v1.34.9 | Historical Linux amd64 and Darwin arm64 binaries passed scoped actual qualification | Exact frozen artifacts only; other native platforms remain untested. |
| OpenShift 4.22 and next development | Untested live targets | Outside the completed historical scope; full-matrix gate remains incomplete. |
| Current maintenance candidates | Source delta assessed; local synthetic and contract checks | Still require new exact-artifact actual qualification; never inherit historical live passes. |

Observed historical coverage includes offline conversion, discovery and JSON protocol handling, read-only planning, server dry run, paused zero-replica staging, exclusive and overlap cutover, Service routing and health checks, HPA retargeting, ImageStream triggers, admission/SCC behavior, restricted permissions, stale-plan refusal, conflict handling, interruption and explicit recovery. Targeted checks also covered namespace isolation, raw-field refusal, network-policy selectors, PV topology, storage ownership and singleton workers.

This does not certify arbitrary workloads or clusters. Preflight is a point-in-time check, not a capacity reservation. CSI attachment/mounting, runtime and hardware profiles, registry trust, external reconcilers and application-specific traffic paths still need operator validation. HTTP and Endpoint checks do not establish UDP, secondary-network or all CNI connectivity. Exclusive workers and volumes require verified source termination before destination activation. Required-SCC use must be authorized for the workload service account.

The [source review](compatibility-review.md) retains official references and conditional patch/runtime prerequisites. [Release policy](release-scope.md) explains immutable historical artifacts and external verification. Raw sessions, extraction dumps and exact hash-pinned overlays belong in a declared external bundle, not the source tree. Missing inputs make qualification unavailable; ordinary builds and tests continue to work.
