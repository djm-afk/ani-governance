-- Run after admin sync-apis --dry-run and explicit sync-apis.
-- Registers exact route permissions only; does not grant roles or plan modules.
BEGIN;
SET LOCAL lock_timeout='5s';
SET LOCAL statement_timeout='45s';
LOCK TABLE sys_permissions,sys_apis,sys_permission_apis IN SHARE ROW EXCLUSIVE MODE;
DO $$
DECLARE r record; aid bigint; pid bigint; route_count bigint;
BEGIN
 FOR r IN SELECT * FROM (VALUES
 ('POST','/api/v1/networks/vnics','NetworkService','NETWORK','network:vnic:create'),
 ('GET','/api/v1/networks/vnics','NetworkService','NETWORK','network:vnic:list'),
 ('GET','/api/v1/networks/vnics/{vnic_id}','NetworkService','NETWORK','network:vnic:get'),
 ('DELETE','/api/v1/networks/vnics/{vnic_id}','NetworkService','NETWORK','network:vnic:delete'),
 ('GET','/api/v1/networks/vnics/{vnic_id}/binding-targets','NetworkService','NETWORK','network:vnic:binding-targets'),
 ('GET','/api/v1/virtual-machines/{vm_id}/network-interfaces','VMService','VIRTUAL_MACHINE','vm:network-interfaces:get'),
 ('PUT','/api/v1/virtual-machines/{vm_id}/network-interfaces/{slot}','VMService','VIRTUAL_MACHINE','vm:network-interfaces:set'),
 ('GET','/api/v1/instances/{instance_id}/network-interfaces','InstanceService','INSTANCE','instance:network-interfaces:get'),
 ('PUT','/api/v1/instances/{instance_id}/network-interfaces/{slot}','InstanceService','INSTANCE','instance:network-interfaces:set')
 ) AS routes(method,path,module,business_module,code) LOOP
  SELECT count(*),min(id) INTO route_count,aid FROM sys_apis
   WHERE path=r.path AND method=r.method AND module=r.module
     AND business_module=r.business_module AND scope='ADMIN'
     AND status='ON' AND deleted_at IS NULL;
  IF route_count<>1 THEN RAISE EXCEPTION 'One registered active route is required for % %',r.method,r.path; END IF;
  INSERT INTO sys_permissions(name,code,status,created_at) VALUES(r.code,r.code,'ON',now()) ON CONFLICT(code) DO NOTHING;
  SELECT id INTO STRICT pid FROM sys_permissions WHERE code=r.code AND status='ON' AND deleted_at IS NULL;
  IF EXISTS(SELECT 1 FROM sys_permission_apis WHERE api_id=aid AND (permission_id<>pid OR deleted_at IS NOT NULL)) THEN
   RAISE EXCEPTION 'Route permission differs or was deleted; explicit recovery required';
  END IF;
  INSERT INTO sys_permission_apis(permission_id,api_id,created_at) VALUES(pid,aid,now()) ON CONFLICT(permission_id,api_id) DO NOTHING;
 END LOOP;
END $$;
COMMIT;
