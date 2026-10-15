# Controlled migration

Generation is read-only against the cluster. Every generated manifest is accompanied by a review plan containing the original DC snapshot, intended replicas, semantics, warnings, blockers and recovery instructions. Offline/generation plans have incomplete dependent-resource inventory and cannot execute.

Create a live plan with `--mode plan --projects example --context TEST_CONTEXT`. This inventories namespace Services, HPAs and controllers without changing them. A later stage revalidates the source UID/resource version, source spec, dependencies and controller overlap. Permissions for optional planning are separate from ordinary generation. Review the plan, pod labels, selected context, dependent resources and warnings before application.

`--mode dry-run --plan-file PATH --context TEST_CONTEXT` submits only a paused, zero-replica destination for server dry run. `--mode stage` performs the same dry run then creates that destination. Image triggers are removed during staging. Existing destinations require explicit source and plan ownership and must still match the admitted staged spec. Foreign objects, changed plans, active destinations and conflicting creates are never overwritten. A journal records creation intent before mutation; a safe rerun discovers a previously created owned destination. The plan uses a local exclusive lock; after process death, inspect the process/cluster/journal before removing a stale `.lock` file.

The old `--apply-changes` flag now aliases staging and requires `--plan-file`; it cannot start a second active workload. Staging does not retire the DC or move traffic. Unsupported semantics produce manual diagnostics and prevent staging. Admission changes to protected identity, selector, images, pod labels, pause or replica count fail the operation. Other defaults/admission changes are recorded for review.

Use private storage for plans: snapshots can contain environment values. Keep the DC for recovery. Cutover and recovery procedures must always stop and verify the destination before restarting an exclusive source; never delete the DC as an automatic cleanup step.
