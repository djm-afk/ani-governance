-- Retire only ACC's rebuildable Usage projection. Original operations,
-- immutable canonical/hash/ACK, charges, release receipts and DELETE
-- idempotency facts remain in their existing tables and foreign-key graph.
DROP TABLE "public"."sys_gpu_usage_sync";
