-- Restore with Gov stopped and the matching old binary; keep all permissions,
-- statuses, role links and plan modules. Only remove the new route mappings.
BEGIN;
SET LOCAL lock_timeout='5s';
LOCK TABLE sys_apis, sys_permission_apis IN SHARE ROW EXCLUSIVE MODE;
DO $$
DECLARE aid bigint;
BEGIN
 IF EXISTS(SELECT 1 FROM sys_apis WHERE path='/api/v1/instance-presets' AND method='GET') THEN RAISE EXCEPTION 'old catalog already exists; inspect state'; END IF;
 SELECT id INTO STRICT aid FROM sys_apis WHERE path='/api/v1/compute-presets' AND method='GET' AND deleted_at IS NULL AND module='ComputePresetService' AND scope='ADMIN' AND business_module='SYSTEM' AND operation='ComputePresetService_ListPresets';
 IF EXISTS(SELECT 1 FROM sys_permission_apis pa JOIN sys_permissions p ON p.id=pa.permission_id WHERE pa.api_id=aid AND pa.deleted_at IS NULL AND p.code NOT IN ('instance:preset:read','modeldev:compute-preset:read','inference:compute-preset:read')) THEN RAISE EXCEPTION 'foreign catalog grant'; END IF;
 DELETE FROM sys_permission_apis WHERE api_id=aid AND permission_id IN (SELECT id FROM sys_permissions WHERE code IN ('modeldev:compute-preset:read','inference:compute-preset:read'));
 UPDATE sys_apis SET path='/api/v1/instance-presets',module='InstanceService',operation='InstanceService_ListInstancePresets',business_module='INSTANCE' WHERE id=aid;
END $$;
COMMIT;
