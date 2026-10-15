# Development and test isolation

Construct commands with `NewCommand()` and test work through a `Runner`. Options, Kubernetes interfaces, logging, output and clock belong to the runner; tests must supply temporary artifact paths. Offline `--input` accepts exported DeploymentConfig YAML, JSON, lists and multiple YAML documents without loading kubeconfig.

`testdata/ocp-4.14-dc.yaml` is synthetic API-shaped data, not evidence from a cluster. Unit tests and HTTP simulations do not qualify OpenShift integration. Never run integration against the current kubeconfig implicitly.

Local checks: `go test -race ./...`, `go vet ./...`. Use a writable `GOCACHE` and `GOMODCACHE` when running in a filesystem sandbox.
