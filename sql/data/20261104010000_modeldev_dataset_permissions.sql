-- Explicitly apply after the reviewed OpenAPI API-catalog sync. No module,
-- plan, tenant or account is created. The MODELDEV entitlement remains
-- independently required: these are ModelDev's own routes, catalogued as
-- MODELDEV. Only routes that read the model repository are MODEL. Default
-- grants cover only the exact tenant-manager roles, matching the environment
-- permission step.
--
-- The two InputVersion read routes move to modeldev:dataset:read in one step.
-- Measured against the deployed database (2026-11-04), they were NOT exempt as
-- the design assumed: they were already bound to modeldev:input:list and
-- modeldev:input:get. The switch therefore has to be a real migration: grants
-- held through the old codes are first carried over to dataset:read, only then
-- are the old links removed. The order matters — it leaves no window where a
-- role that could read yesterday cannot read now.
BEGIN;
DO $$
DECLARE item record; selected_api_id bigint; selected_permission_id bigint; route_count bigint;
        read_permission_id bigint;
BEGIN
 -- 1. Create every capability first so the carry-over below can reference it.
 FOR item IN SELECT * FROM (VALUES
  ('modeldev:dataset:create'), ('modeldev:dataset:read'), ('modeldev:dataset:update'),
  ('modeldev:dataset:archive'), ('modeldev:dataset:import')
 ) AS codes(code) LOOP
  INSERT INTO public.sys_permissions(name,code,status) VALUES(item.code,item.code,'ON') ON CONFLICT(code) DO NOTHING;
 END LOOP;
 SELECT p.id INTO STRICT read_permission_id FROM public.sys_permissions p
  WHERE p.code='modeldev:dataset:read' AND p.deleted_at IS NULL;

 -- 2. Carry over every live grant the old InputVersion read codes hold, so the
 --    switch below cannot take a working read away from an existing role.
 INSERT INTO public.sys_role_permissions(tenant_id,role_id,permission_id,status,effect)
  SELECT rp.tenant_id,rp.role_id,read_permission_id,rp.status,rp.effect
   FROM public.sys_role_permissions rp
   JOIN public.sys_permissions p ON p.id=rp.permission_id
  WHERE p.code IN ('modeldev:input:list','modeldev:input:get') AND rp.deleted_at IS NULL
  ON CONFLICT DO NOTHING;

 -- 3. Bind the seventeen routes, then retire the two superseded links.
 FOR item IN SELECT * FROM (VALUES
  ('POST',  '/api/v1/modeldev/datasets',                                  'modeldev:dataset:create'),
  ('GET',   '/api/v1/modeldev/datasets',                                  'modeldev:dataset:read'),
  ('GET',   '/api/v1/modeldev/datasets/{dataset_id}',                     'modeldev:dataset:read'),
  ('PATCH', '/api/v1/modeldev/datasets/{dataset_id}',                     'modeldev:dataset:update'),
  ('POST',  '/api/v1/modeldev/datasets/{dataset_id}:archive',             'modeldev:dataset:archive'),
  ('POST',  '/api/v1/modeldev/datasets/{dataset_id}:restore',             'modeldev:dataset:archive'),
  ('POST',  '/api/v1/modeldev/dataset-sources:inspect',                   'modeldev:dataset:import'),
  ('POST',  '/api/v1/modeldev/datasets/{dataset_id}:import',              'modeldev:dataset:import'),
  ('GET',   '/api/v1/modeldev/datasets/{dataset_id}/versions',            'modeldev:dataset:read'),
  ('GET',   '/api/v1/modeldev/input-versions/{input_version_id}/preview', 'modeldev:dataset:read'),
  ('GET',   '/api/v1/modeldev/datasets/{dataset_id}/import-tasks',        'modeldev:dataset:read'),
  ('GET',   '/api/v1/modeldev/dataset-import-tasks/{import_task_id}',     'modeldev:dataset:read'),
  ('POST',  '/api/v1/modeldev/dataset-import-tasks/{import_task_id}:cancel','modeldev:dataset:import'),
  ('POST',  '/api/v1/modeldev/dataset-import-tasks/{import_task_id}:retry', 'modeldev:dataset:import'),
  ('GET',   '/api/v1/modeldev/datasets/{dataset_id}/operations',          'modeldev:dataset:read'),
  -- The two InputVersion reads that were bound to modeldev:input:list/get.
  ('GET',   '/api/v1/modeldev/input-versions',                            'modeldev:dataset:read'),
  ('GET',   '/api/v1/modeldev/input-versions/{input_version_id}',         'modeldev:dataset:read')
 ) AS contracts(method,path,code) LOOP
  SELECT count(*),min(a.id) INTO route_count,selected_api_id FROM public.sys_apis a
   WHERE a.path=item.path AND a.method=item.method AND a.deleted_at IS NULL
    AND a.scope='ADMIN' AND a.business_module='MODELDEV' AND a.status='ON';
  IF route_count<>1 THEN RAISE EXCEPTION 'ModelDev dataset requires exactly one active catalog API for % %',item.method,item.path;END IF;
  SELECT p.id INTO STRICT selected_permission_id FROM public.sys_permissions p WHERE p.code=item.code AND p.deleted_at IS NULL;
  -- One route belongs to exactly one live capability. Any other live binding is
  -- retired here only when it is one of the two superseded read codes; anything
  -- else still means the catalog disagrees and the script stops.
  UPDATE public.sys_permission_apis pa
     SET deleted_at=NOW()
    FROM public.sys_permissions old
   WHERE pa.api_id=selected_api_id AND pa.deleted_at IS NULL
     AND old.id=pa.permission_id AND old.id<>selected_permission_id
     AND old.code IN ('modeldev:input:list','modeldev:input:get');
  IF EXISTS(SELECT 1 FROM public.sys_permission_apis pa WHERE pa.api_id=selected_api_id AND pa.permission_id<>selected_permission_id AND pa.deleted_at IS NULL) THEN
   RAISE EXCEPTION 'ModelDev dataset API % % has a different permission',item.method,item.path;
  END IF;
  INSERT INTO public.sys_permission_apis(permission_id,api_id) VALUES(selected_permission_id,selected_api_id) ON CONFLICT DO NOTHING;
  INSERT INTO public.sys_role_permissions(tenant_id,role_id,permission_id,status,effect)
   SELECT r.tenant_id,r.id,selected_permission_id,'ON','ALLOW' FROM public.sys_roles r
   WHERE r.deleted_at IS NULL AND r.status='ON' AND
    ((r.tenant_id=0 AND r.type='TEMPLATE' AND r.code='template:tenant:manager') OR
     (r.tenant_id>0 AND r.type='TENANT' AND r.code='tenant:manager'))
   ON CONFLICT DO NOTHING;
 END LOOP;
END $$;
COMMIT;
