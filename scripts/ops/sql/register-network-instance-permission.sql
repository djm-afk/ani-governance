-- Run explicitly after admin sync-apis --dry-run and approved sync-apis.
-- Registers this exact NETWORK read permission; does not grant roles, plans
-- or API keys, change tenant membership, or execute on application startup.
BEGIN;
SET LOCAL lock_timeout='5s';
SET LOCAL statement_timeout='45s';
LOCK TABLE sys_permissions,sys_apis,sys_permission_apis IN SHARE ROW EXCLUSIVE MODE;
DO $$
DECLARE aid bigint; pid bigint; route_count bigint;
BEGIN
 SELECT count(*),min(id) INTO route_count,aid FROM sys_apis
  WHERE path='/api/v1/networks/instances' AND method='GET' AND module='NetworkService'
    AND business_module='NETWORK' AND scope='ADMIN' AND status='ON' AND deleted_at IS NULL;
 IF route_count<>1 THEN
  RAISE EXCEPTION 'One active GET /api/v1/networks/instances route is required';
 END IF;
 INSERT INTO sys_permissions(name,code,status,created_at)
  VALUES('network:instance:list','network:instance:list','ON',now()) ON CONFLICT(code) DO NOTHING;
 SELECT id INTO STRICT pid FROM sys_permissions
  WHERE code='network:instance:list' AND status='ON' AND deleted_at IS NULL;
 IF EXISTS(SELECT 1 FROM sys_permission_apis WHERE api_id=aid AND (permission_id<>pid OR deleted_at IS NOT NULL)) THEN
  RAISE EXCEPTION 'Network instance route permission differs or was deleted; explicit recovery required';
 END IF;
 INSERT INTO sys_permission_apis(permission_id,api_id,created_at)
  VALUES(pid,aid,now()) ON CONFLICT(permission_id,api_id) DO NOTHING;
END $$;
COMMIT;
