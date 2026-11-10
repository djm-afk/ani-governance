# GPU contract v1.1 cutover

This batch enables the new GPU business contract for Res container instances.
ModelDev and Inference retain CPU admission; their new GPU requests fail before
ACC resolution or quota occupancy. Monitoring and capacity never authorize a
CREATE and never delay owner cleanup or the original charge refund.

## Before the maintenance window

Use the existing deployment and backup procedure in [deployment.md](../deployment.md).
Record the actual cluster, namespace, running images, configuration and database
schema/revisions before changing them. The target for this batch is the verified
66 environment; source-main migration files do not prove that target has applied
them. In particular, an existing `sys_quota_operations.instance_contract_version`
column indicates that the corresponding historical migration needs reconciliation.
Do not baseline over a schema difference or assume the last main migration ran.

Back up the database and the matched four-service configuration/images. Keep
credentials, raw environment values, cookies and tokens out of evidence.

1. Close this batch's GPU supply-group admission through the old management API.
   Save the original administrator settings. Finish this task's in-flight calls.
2. Run [the read-only Gov inventory](../../scripts/ops/sql/preflight-gpu-contract-v11.sql)
   using a read-only database session. Record exact original CREATE/DELETE and
   charge IDs in the combined cross-service manifest; an empty result is an
   empty inventory, not a requirement to manufacture GPU business records.
3. Close listed old GPU business through the original owner deletion/cancellation
   path, including ModelDev/Inference when present. Gov refunds each original
   charge through its existing release transaction. UNKNOWN/retry-blocked calls
   remain unresolved. Do not force dispatch states or edit account balances.
4. Re-run the inventory. All related command deliveries must be terminal, all
   original quota items must have their matching charge, and **all** charges of
   those GPU CREATEs (including a mixed non-GPU item) must be fully released.
   Check the owner refund outboxes separately in their owner databases.
5. Stop old Gov dispatch/projection writers after closure. Record remaining
   derived UsageSync rows in the manifest; their retirement does not erase an
   original owner release event or receipt. Keep the old version available only
   for the matched backup recovery procedure.

## Explicit schema and data steps

Review Atlas status against the actual target revisions and schema. Apply any
missing, already-reviewed historical migrations in their established order,
then apply `20261110010000_retire_acc_usage_sync.sql`. Do not edit applied files
or checksums. The new migration drops only `sys_gpu_usage_sync`, without CASCADE.
It preserves `sys_quota_operations`, charges, accounts, release receipts and
`sys_gpu_delete_acceptances`, including the latter's restrictive foreign keys
onto the original CREATE and DELETE operations.

Gov never deletes or rewrites original `canonical_request`, `request_hash`,
`ack_json`, charge IDs/amounts or release receipts in this cutover. Closed old
GPU canonical payloads remain immutable accounting history. They are not
converted to BusinessSpec, decoded for execution or queued again. CPU historical
requests keep their original canonical bytes and replay path. Res owns the
separate manifest-scoped archive of its closed runtime snapshots and refund
outbox relationships; Gov migration does not remove a parent ledger row to
work around those references.

Deploy the matched Obs/ACC/Res/Gov versions and collection configuration. Run
the same-version `admin sync-apis --dry-run`, explicit synchronization, and the
reviewed accelerator permission SQL. Refresh the existing policy engine as
specified in deployment.md. Retire only the seven old accelerator paths and
their exclusive grants; retain shared GPU monitoring permissions and unrelated
roles/modules. Restore the saved supply admission settings after internal RPCs
and the new backend APIs are verified.

## Post-cutover verification

Verify CREATE retry, unsent CREATE cancellation, owner cleanup and original
charge refund using actual Gov/Res APIs. Verify CPU replay and the explicit
ModelDev/Inference GPU rejection. Test the two new platform capacity GETs through
the real HTTP encoder: absent 64-bit values are `null`, known zero is `"0"`, and
nested details preserve presence. Tenant users cannot call these admin routes.

RESOURCE GPU monitoring uses the authenticated tenant, current resource-read
permission and Gov's original UsageRef. It does not require a live instance or
Pod. Refunded original charges still identify recorded history. Tenant usage
states `observation_basis=EXPORTER_OBSERVED` and
`owner_scope=ani-resource-service`; it does not replace the quota ledger or claim
a complete tenant allocation inventory.

If rollback is necessary, stop the new version and restore the same backup,
old schemas, binaries and configuration as one matched set. Do not put a new
binary on old schema or create a runtime dual-contract fallback.
