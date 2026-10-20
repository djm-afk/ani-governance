-- Explicit unified catalog upgrade. Run with Gov stopped, before sync-apis.
-- Existing API ID, container permission, status and role links are preserved.
-- Fresh installs first sync the new route. Never grants roles or plan modules.
BEGIN;
SET LOCAL lock_timeout='5s';
SET LOCAL statement_timeout='45s';
LOCK TABLE sys_apis, sys_permissions, sys_permission_apis IN SHARE ROW EXCLUSIVE MODE;
DO $$
DECLARE aid bigint; pid bigint; old_id bigint; new_id bigint; code_value text;
BEGIN
 IF (SELECT count(*) FROM sys_apis WHERE path IN ('/api/v1/instance-presets','/api/v1/compute-presets') AND method='GET') <> 1 THEN RAISE EXCEPTION 'catalog missing or ambiguous'; END IF;
 SELECT id INTO old_id FROM sys_apis WHERE path='/api/v1/instance-presets' AND method='GET';
 SELECT id INTO new_id FROM sys_apis WHERE path='/api/v1/compute-presets' AND method='GET';
 IF old_id IS NOT NULL AND new_id IS NOT NULL THEN RAISE EXCEPTION 'catalog path conflict'; END IF;
 aid := coalesce(old_id,new_id);
 IF aid IS NULL OR NOT EXISTS(SELECT 1 FROM sys_apis WHERE id=aid AND deleted_at IS NULL AND scope='ADMIN'
 AND ((path='/api/v1/instance-presets' AND module='InstanceService' AND operation='InstanceService_ListInstancePresets' AND business_module='INSTANCE')
 OR (path='/api/v1/compute-presets' AND module='ComputePresetService' AND operation='ComputePresetService_ListPresets' AND business_module='SYSTEM'))) THEN
 RAISE EXCEPTION 'registered catalog missing or unexpected owner'; END IF;
 IF EXISTS(SELECT 1 FROM sys_permission_apis pa JOIN sys_permissions p ON p.id=pa.permission_id WHERE pa.api_id=aid AND pa.deleted_at IS NULL
 AND p.code NOT IN ('instance:preset:read','modeldev:compute-preset:read','inference:compute-preset:read')) THEN RAISE EXCEPTION 'foreign catalog grant'; END IF;
 UPDATE sys_apis SET path='/api/v1/compute-presets',module='ComputePresetService',operation='ComputePresetService_ListPresets',business_module='SYSTEM'
 WHERE id=aid AND (path,module,operation,business_module) IS DISTINCT FROM ('/api/v1/compute-presets','ComputePresetService','ComputePresetService_ListPresets','SYSTEM');
 FOREACH code_value IN ARRAY ARRAY['instance:preset:read','modeldev:compute-preset:read','inference:compute-preset:read'] LOOP
  INSERT INTO sys_permissions(name,code,status,created_at) VALUES(code_value,code_value,'ON',now()) ON CONFLICT(code) DO NOTHING;
  SELECT id INTO STRICT pid FROM sys_permissions WHERE code=code_value AND deleted_at IS NULL;
  IF EXISTS(SELECT 1 FROM sys_permission_apis WHERE permission_id=pid AND api_id=aid AND deleted_at IS NOT NULL) THEN RAISE EXCEPTION 'deleted catalog mapping'; END IF;
  INSERT INTO sys_permission_apis(permission_id,api_id,created_at) VALUES(pid,aid,now()) ON CONFLICT(permission_id,api_id) DO NOTHING;
 END LOOP;
END $$;
COMMIT;
