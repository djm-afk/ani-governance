-- Apply explicitly after OpenAPI catalog dry-run and sync, inside a transaction.
-- Registers one platform read capability only. It never grants a role, enables
-- an existing permission, or expands quota-read permissions.
DO $$
DECLARE selected_permission_id bigint; selected_api_id bigint; route_count bigint; route text;
BEGIN
 INSERT INTO sys_permissions(name,code,status) VALUES('平台资源使用统计','resource-usage:read','OFF') ON CONFLICT(code) DO NOTHING;
 SELECT id INTO STRICT selected_permission_id FROM sys_permissions WHERE code='resource-usage:read' AND deleted_at IS NULL;
 FOREACH route IN ARRAY ARRAY['/api/v1/admin/resource-usage/overview','/api/v1/admin/resource-usage/tenants','/api/v1/admin/resource-usage/tenants/{tenantId}'] LOOP
  SELECT count(*),min(id) INTO route_count,selected_api_id FROM sys_apis WHERE method='GET' AND path=route AND deleted_at IS NULL AND business_module='DASHBOARD' AND status='ON';
  IF route_count<>1 THEN RAISE EXCEPTION 'Expected one active ResourceUsage GET route: %',route; END IF;
  IF EXISTS(SELECT 1 FROM sys_permission_apis WHERE api_id=selected_api_id AND permission_id<>selected_permission_id) THEN RAISE EXCEPTION 'Resource usage route has foreign permission: %',route; END IF;
  INSERT INTO sys_permission_apis(permission_id,api_id) VALUES(selected_permission_id,selected_api_id) ON CONFLICT DO NOTHING;
 END LOOP;
END $$;
