-- Cluster operations: run AFTER same-version admin sync-apis --dry-run and sync-apis.
-- Only the eleven fixed APIs and seven capabilities below are registered.
-- Grant only the existing active SYSTEM platform:admin role in tenant 0.
-- Does not create roles, change plans/API Keys, or replay fresh-install admin init.
-- Any missing/disabled/conflicting catalog or grant aborts the whole transaction.
BEGIN;
SET LOCAL lock_timeout='5s';
SET LOCAL statement_timeout='45s';
DO $$
DECLARE
  r record;
  aid bigint;
  pid bigint;
  route_count bigint;
  platform_role_id bigint;
  route_ids bigint[] := ARRAY[]::bigint[];
  permission_ids bigint[] := ARRAY[]::bigint[];
BEGIN
  SELECT count(*),min(id) INTO route_count,platform_role_id FROM sys_roles
   WHERE tenant_id=0 AND code='platform:admin' AND type='SYSTEM'
     AND status='ON' AND deleted_at IS NULL;
  IF route_count<>1 THEN
    RAISE EXCEPTION 'existing active SYSTEM platform:admin role in tenant 0 is required';
  END IF;

  FOR r IN SELECT * FROM (VALUES
    ('GET','/api/v1/admin/cluster-ops/clusters','cluster:ops:read','查看集群运维资源'),
    ('GET','/api/v1/admin/cluster-ops/clusters/{cluster_id}/overview','cluster:ops:read','查看集群运维资源'),
    ('GET','/api/v1/admin/cluster-ops/clusters/{cluster_id}/resource-types','cluster:ops:read','查看集群运维资源'),
    ('GET','/api/v1/admin/cluster-ops/clusters/{cluster_id}/resources','cluster:ops:read','查看集群运维资源'),
    ('GET','/api/v1/admin/cluster-ops/clusters/{cluster_id}/resources/{name}','cluster:ops:read','查看集群运维资源'),
    ('POST','/api/v1/admin/cluster-ops/clusters/{cluster_id}/resources','cluster:ops:create','创建集群运维资源'),
    ('PUT','/api/v1/admin/cluster-ops/clusters/{cluster_id}/resources/{name}','cluster:ops:update','修改集群运维资源'),
    ('DELETE','/api/v1/admin/cluster-ops/clusters/{cluster_id}/resources/{name}','cluster:ops:delete','删除集群运维资源'),
    ('POST','/api/v1/admin/cluster-ops/clusters/{cluster_id}/resources/{name}/scale','cluster:ops:scale','调整工作负载副本数'),
    ('POST','/api/v1/admin/cluster-ops/clusters/{cluster_id}/resources/{name}/restart','cluster:ops:restart','提交工作负载重启'),
    ('GET','/api/v1/admin/cluster-ops/clusters/{cluster_id}/namespaces/{namespace}/pods/{pod_name}/logs','cluster:ops:logs','读取集群Pod日志')
  ) AS routes(method,path,code,name) LOOP
    SELECT count(*) INTO route_count FROM sys_apis
     WHERE path=r.path AND method=r.method AND deleted_at IS NULL;
    IF route_count<>1 THEN
      RAISE EXCEPTION 'cluster operations API must be synced exactly once: % %', r.method, r.path;
    END IF;
    SELECT count(*),min(id) INTO route_count,aid FROM sys_apis
     WHERE path=r.path AND method=r.method AND module='PlatformClusterOpsService'
       AND business_module='SYSTEM' AND scope='ADMIN' AND status='ON' AND deleted_at IS NULL;
    IF route_count<>1 THEN
      RAISE EXCEPTION 'cluster operations API metadata/status differs from the release: % %', r.method, r.path;
    END IF;
    route_ids := array_append(route_ids,aid);

    INSERT INTO sys_permissions(name,code,status,created_at)
    VALUES(r.name,r.code,'ON',now()) ON CONFLICT(code) DO NOTHING;
    SELECT count(*),min(id) INTO route_count,pid FROM sys_permissions
     WHERE code=r.code AND status='ON' AND deleted_at IS NULL;
    IF route_count<>1 THEN
      RAISE EXCEPTION 'cluster operations capability is disabled or unavailable: %', r.code;
    END IF;
    permission_ids := array_append(permission_ids,pid);
    IF EXISTS(SELECT 1 FROM sys_permission_apis
       WHERE api_id=aid AND permission_id<>pid AND deleted_at IS NULL) THEN
      RAISE EXCEPTION 'cluster operations API has a foreign permission: % %', r.method, r.path;
    END IF;
    INSERT INTO sys_permission_apis(permission_id,api_id,created_at)
    VALUES(pid,aid,now()) ON CONFLICT(permission_id,api_id) DO NOTHING;
    IF NOT EXISTS(SELECT 1 FROM sys_permission_apis
       WHERE api_id=aid AND permission_id=pid AND deleted_at IS NULL) THEN
      RAISE EXCEPTION 'cluster operations permission/API association is inactive: % %', r.method, r.path;
    END IF;
  END LOOP;

  -- Reusing one of these codes must never grant unrelated APIs to the role.
  IF EXISTS(SELECT 1 FROM sys_permission_apis
     WHERE permission_id=ANY(permission_ids) AND NOT (api_id=ANY(route_ids)) AND deleted_at IS NULL) THEN
    RAISE EXCEPTION 'cluster operations capability is associated with an unrelated API';
  END IF;
  FOREACH pid IN ARRAY permission_ids LOOP
    INSERT INTO sys_role_permissions(created_at,tenant_id,status,role_id,permission_id,effect,priority)
    VALUES(now(),0,'ON',platform_role_id,pid,'ALLOW',0) ON CONFLICT DO NOTHING;
    IF NOT EXISTS(SELECT 1 FROM sys_role_permissions
       WHERE tenant_id=0 AND role_id=platform_role_id AND permission_id=pid
         AND status='ON' AND effect='ALLOW' AND deleted_at IS NULL) THEN
      RAISE EXCEPTION 'cluster operations platform administrator grant is inactive or denied';
    END IF;
  END LOOP;
END $$;
COMMIT;

-- Review the resulting module only; cache refresh and re-login remain required.
SELECT a.method,a.path,a.module,a.business_module,p.code
  FROM sys_apis a
  JOIN sys_permission_apis pa ON pa.api_id=a.id AND pa.deleted_at IS NULL
  JOIN sys_permissions p ON p.id=pa.permission_id AND p.deleted_at IS NULL
 WHERE p.code IN ('cluster:ops:read','cluster:ops:create','cluster:ops:update','cluster:ops:delete',
                  'cluster:ops:scale','cluster:ops:restart','cluster:ops:logs')
   AND a.deleted_at IS NULL
 ORDER BY a.method,a.path;
SELECT rp.tenant_id,r.code,p.code,rp.status,rp.effect
  FROM sys_role_permissions rp
  JOIN sys_roles r ON r.id=rp.role_id
  JOIN sys_permissions p ON p.id=rp.permission_id
 WHERE p.code IN ('cluster:ops:read','cluster:ops:create','cluster:ops:update','cluster:ops:delete',
                  'cluster:ops:scale','cluster:ops:restart','cluster:ops:logs')
   AND rp.deleted_at IS NULL
 ORDER BY rp.tenant_id,r.code,p.code;
