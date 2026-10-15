# Conversion behavior

The baseline is OpenShift 4.14 (Kubernetes 1.27). The embedded field/required-property catalog is derived from the official Kubernetes v1.27.16 OpenAPI document; its URL and original SHA-256 are recorded in `compatibility/baseline-1.27.json`. Regenerate with `tools/extract-baseline.py`. This checks baseline field availability and required properties; typed decoding and local checks supplement it. It does not replace server validation, admission or OpenShift controller tests.

| DC feature | Output and operator action |
| --- | --- |
| Pod labels | Retained independently of metadata flags, including Service-facing `deploymentconfig`; add a stable migration selector label |
| Replicas | Explicit zero retained; omission defaults to one |
| Paused, minReadySeconds, revisionHistoryLimit | Retained; omitted readiness/history use Deployment defaults (0 and 10) |
| Rolling | RollingUpdate; preserve integer/percentage limits; missing limits default to 25% |
| Recreate | Recreate |
| Custom/unknown, test DC, hooks | Paused review manifest, actionable manual classification, nonzero exit, no automatic application |
| Automatic ImageChange | `image.openshift.io/triggers` per container, preserving source namespace and paused/disabled state |
| Unsupported/ambiguous ImageChange | Manual classification, no automatic application |
| ConfigChange | Deployment template changes cause rollouts |
| Omitted/null triggers | DC API ConfigChange default; warning recorded |
| Empty/no ConfigChange triggers | Paused review manifest; manual rollout procedure required |
| DC rollback | Deployment provides no equivalent automatic rollback guarantee; explicit recovery required |
| Deployer timing/deadlines | Not equivalent; warning recorded; nonempty custom deployer resources/metadata requires manual work |
| Unknown DC semantics/newer pod fields | Manual classification or validation error; never silently dropped |

Migration identifiers derive from source namespace, name and UID. Output contains no source owner references, resource version or server status. Conversion never modifies the source object. Hook Jobs, custom deployers and dependent-resource transitions require operator design; the tool does not invent substitutes.
