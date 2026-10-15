# Permissions by operation

All live modes discover stable API resource lists and create `authorization.k8s.io/v1` SelfSubjectAccessReviews. Ordinary generation needs only list access to `apps.openshift.io/v1` DeploymentConfigs in requested namespaces, plus API discovery. It does not GET namespace objects or cluster-version metadata. Reserved namespaces are filtered first. Offline input needs no cluster credentials.

| Operation | Additional namespace permissions |
| --- | --- |
| Live plan | list core Services/RCs, apps Deployments/ReplicaSets/StatefulSets/DaemonSets, batch Jobs, autoscaling/v2 HPAs; get source DC and get known Services/HPAs during revalidation |
| Dry run and stage | get source DC, get/create Deployments, all live-plan revalidation permissions; discover ImageStreamTag API if image triggers are used |
| Cutover/recovery | get/update DCs and Deployments; list pods, RCs, ReplicaSets; get Services and Endpoints |
| HPA cutover/recovery | get/delete/create HPAs, with original UID/version preconditions and retained snapshots |
| Integration harness only | CRUD test DCs, Deployments, Services, Routes, ImageStreams/Tags, HPAs and ResourceQuotas; list pods/RCs/RS; get the pre-labelled test namespace and read ClusterVersion/server metadata |

Create/update dry runs require the same create/update permission as a real request. Denied access reviews, unavailable APIs and real operation/admission failures all fail explicitly. Namespace-scoped RoleBindings suffice for normal operation; cluster-scoped namespace/version reads belong only to qualification setup.

The core v1 Endpoints API is used as the 4.14 routing baseline. Later optional APIs are never assumed from a version number. If a required capability is absent, execution stops with a diagnostic. Deprecation warnings remain informational; permission, schema and admission errors are retained.

Do not grant namespace deletion, privileged SCCs or cluster-admin to the converter. Pause external reconcilers and scalers before cutover. The local plan lock coordinates one process only; it is not a cluster-wide lease.
