-- Explicit P4 step after backing up the named API/permission tables.
-- Never run at startup. No ledger, balance, charge, receipt or owner data changes.
BEGIN;
CREATE TEMP TABLE ani_acc_v11_retired_apis ON COMMIT DROP AS
 SELECT id FROM sys_apis WHERE (method,path) IN (
  ('GET','/api/v1/admin/accelerator/bindings'),
  ('GET','/api/v1/admin/accelerator/capacity'),
  ('GET','/api/v1/accelerator/capacity'),
  ('POST','/api/v1/accelerator/admission-preview'),
  ('GET','/api/v1/accelerator/usages'),
  ('GET','/api/v1/accelerator/usages/{owner_service}/{resource_id}'),
  ('GET','/api/v1/accelerator/usages/{owner_service}/{resource_id}/bindings'),
  ('GET','/admin/v1/accelerator/bindings'),
  ('GET','/admin/v1/accelerator/capacity')
 );
DELETE FROM sys_permission_apis WHERE api_id IN (SELECT id FROM ani_acc_v11_retired_apis);
DELETE FROM sys_apis WHERE id IN (SELECT id FROM ani_acc_v11_retired_apis);
CREATE TEMP TABLE ani_acc_v11_retired_permissions ON COMMIT DROP AS
 SELECT p.id FROM sys_permissions p WHERE p.code IN (
  'accelerator:binding:admin-list','accelerator:capacity:admin-get',
  'accelerator:capacity:get','accelerator:admission:preview','accelerator:binding:list'
 ) AND NOT EXISTS (SELECT 1 FROM sys_permission_apis pa WHERE pa.permission_id=p.id);
-- Remove only relations owned by these proven-exclusive retired permissions.
-- Historical permission audit logs remain immutable.
DELETE FROM sys_role_permissions WHERE permission_id IN (SELECT id FROM ani_acc_v11_retired_permissions);
DELETE FROM sys_permission_menus WHERE permission_id IN (SELECT id FROM ani_acc_v11_retired_permissions);
DELETE FROM sys_permission_policies WHERE permission_id IN (SELECT id FROM ani_acc_v11_retired_permissions);
DELETE FROM sys_permissions WHERE id IN (SELECT id FROM ani_acc_v11_retired_permissions);
COMMIT;
