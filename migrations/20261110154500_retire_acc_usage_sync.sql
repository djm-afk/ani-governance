-- Target schema reconciliation: an older deployment may record the historical
-- drop_instance_contract_version revision while retaining its unused placeholder.
-- Drop only that constraint/column; preserve every operation, immutable canonical,
-- request hash, ACK, charge, receipt and DELETE foreign-key relationship.
-- IF EXISTS also accepts a fresh database with the historical drop already applied.
ALTER TABLE "public"."sys_quota_operations"
 DROP CONSTRAINT IF EXISTS "sys_quota_operations_instance_contract_version_ck",
 DROP COLUMN IF EXISTS "instance_contract_version";

-- Retire only ACC's rebuildable Usage projection. Original operations,
-- immutable canonical/hash/ACK, charges, release receipts and DELETE
-- idempotency facts remain in their existing tables and foreign-key graph.
DROP TABLE "public"."sys_gpu_usage_sync";
