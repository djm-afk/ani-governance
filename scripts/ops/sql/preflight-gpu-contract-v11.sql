-- Read-only cutover inventory. Run against the backed-up target database with
-- old GPU admission closed; finish these exact old operations through their
-- normal owners before stopping the old workers. This never changes a hash,
-- canonical, ACK, charge, receipt, account or dispatch state.
\set ON_ERROR_STOP on
BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;
WITH gpu_create AS (
 SELECT o.* FROM sys_quota_operations o
 WHERE o.create_operation_id IS NULL AND (
  EXISTS (SELECT 1 FROM sys_quota_charges c WHERE c.tenant_id=o.tenant_id AND c.operation_id=o.operation_id AND c.quota_code IN ('gpu.physical.count','gpu.shared_memory_mib'))
  OR (o.canonical_request::jsonb->'gpu_request' IS NOT NULL AND o.canonical_request::jsonb->'gpu_request' <> 'null'::jsonb)
  OR o.canonical_request::jsonb->>'schema'='ani.modeldev.gpu-ledger.v2.1'
 )
), related AS (
 SELECT o.* FROM sys_quota_operations o WHERE EXISTS (SELECT 1 FROM gpu_create g WHERE g.tenant_id=o.tenant_id AND (g.operation_id=o.operation_id OR g.operation_id=o.create_operation_id))
)
SELECT jsonb_build_object('operation_id',o.operation_id,'create_operation_id',o.create_operation_id,'tenant_id',o.tenant_id,'resource_tenant_id',o.resource_tenant_id,'owner',o.owner_service,'resource_id',o.resource_id,'action',o.action,'dispatch_state',o.dispatch_state,'retry_blocked',o.retry_blocked,'last_error_code',o.last_error_code,'charges',COALESCE((SELECT jsonb_agg(jsonb_build_object('charge_id',c.charge_id,'quota_code',c.quota_code,'original_units',c.original_units,'released_units',c.released_units) ORDER BY c.charge_id) FROM sys_quota_charges c WHERE c.tenant_id=o.tenant_id AND c.operation_id=COALESCE(o.create_operation_id,o.operation_id)),'[]'::jsonb)) AS gpu_contract_v11_object
FROM related o ORDER BY o.tenant_id,o.operation_id;
DO $$
DECLARE pending_ids text; outstanding_ids text; missing_charge_ids text;
BEGIN
 WITH gpu_create AS (
 SELECT o.* FROM sys_quota_operations o WHERE o.create_operation_id IS NULL AND (
 EXISTS (SELECT 1 FROM sys_quota_charges c WHERE c.tenant_id=o.tenant_id AND c.operation_id=o.operation_id AND c.quota_code IN ('gpu.physical.count','gpu.shared_memory_mib'))
 OR (o.canonical_request::jsonb->'gpu_request' IS NOT NULL AND o.canonical_request::jsonb->'gpu_request'<>'null'::jsonb)
 OR o.canonical_request::jsonb->>'schema'='ani.modeldev.gpu-ledger.v2.1')),
 related AS (SELECT o.* FROM sys_quota_operations o WHERE EXISTS (SELECT 1 FROM gpu_create g WHERE g.tenant_id=o.tenant_id AND (g.operation_id=o.operation_id OR g.operation_id=o.create_operation_id)))
 SELECT string_agg(operation_id,',' ORDER BY operation_id) INTO pending_ids FROM related WHERE dispatch_state NOT IN ('ACKED','CANCELED_UNSENT','REJECTED');
 SELECT string_agg(c.charge_id,',' ORDER BY c.charge_id) INTO outstanding_ids
 FROM sys_quota_charges c JOIN sys_quota_operations o ON o.tenant_id=c.tenant_id AND o.operation_id=c.operation_id
 WHERE c.original_units<>c.released_units AND (
 EXISTS(SELECT 1 FROM sys_quota_charges gpu WHERE gpu.tenant_id=o.tenant_id AND gpu.operation_id=o.operation_id AND gpu.quota_code IN ('gpu.physical.count','gpu.shared_memory_mib'))
 OR (o.canonical_request::jsonb->'gpu_request' IS NOT NULL AND o.canonical_request::jsonb->'gpu_request'<>'null'::jsonb)
 OR o.canonical_request::jsonb->>'schema'='ani.modeldev.gpu-ledger.v2.1');
 SELECT string_agg(o.operation_id,',' ORDER BY o.operation_id) INTO missing_charge_ids FROM sys_quota_operations o
 WHERE o.create_operation_id IS NULL AND EXISTS (
 SELECT 1 FROM jsonb_array_elements(COALESCE(o.canonical_request::jsonb->'quota_items','[]'::jsonb)) q
 WHERE q->>'quota_code' IN ('gpu.physical.count','gpu.shared_memory_mib')
 AND NOT EXISTS(SELECT 1 FROM sys_quota_charges c WHERE c.tenant_id=o.tenant_id AND c.operation_id=o.operation_id AND c.quota_code=q->>'quota_code' AND c.original_units=(q->>'units')::bigint));
 IF missing_charge_ids IS NOT NULL THEN RAISE EXCEPTION 'GPU cutover has inconsistent original charge vectors: %',missing_charge_ids; END IF;
 IF pending_ids IS NOT NULL THEN RAISE EXCEPTION 'GPU cutover has unresolved original operations: %',pending_ids; END IF;
 IF outstanding_ids IS NOT NULL THEN RAISE EXCEPTION 'GPU cutover has unreleased original charges: %',outstanding_ids; END IF;
END $$;
COMMIT;
