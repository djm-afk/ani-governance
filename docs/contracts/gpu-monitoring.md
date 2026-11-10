# GPU monitoring: original business identity

This contract describes the current read interfaces. GPU business metrics come from retained Prometheus records labeled by Obs from the original owner business identity. ACC supplies device facts and static catalogue only. Deployment evidence and actual history coverage remain separate acceptance facts.

## Public reads

All methods require a user JWT, current role permission and the registered module/plan gate. API keys are not enabled by this slice. Existing roles, users, permission status, plans and quota policies are not changed by API registration.

| Method and path | Capability and scope |
|---|---|
| GET `/api/v1/admin/gpu/devices` | `accelerator:device:list`, platform; `clusterId`, optional `modelKey`, `page`, `pageSize` |
| GET `/api/v1/admin/gpu/devices/{device_id}/metrics/latest` | `accelerator:device:list`, platform; `clusterId`, optional `metricCodes` |
| POST `/api/v1/admin/gpu/devices/{device_id}/metrics/query` | Same capability; one ACC-registered device |
| GET/POST `/api/v1/admin/gpu/metrics/latest` or `metrics/query` | Same capability; controlled cluster/model group; utilization requires one model |
| GET/POST `/api/v1/admin/gpu/tenants/{tenant_id}/metrics/latest` or `metrics/query` | `tenant:quota-read`, platform tenant aggregate; no resource detail browsing |
| GET/POST `/api/v1/gpu/metrics/latest` or `metrics/query` | `accelerator:usage:list`, authenticated tenant aggregate |
| GET/POST `/api/v1/gpu/resources/{resource_kind}/{resource_id}/metrics/latest` or `metrics/query` | `accelerator:usage:get`, authenticated tenant; also current owner-type resource-read permission |
| GET `/api/v1/admin/tenants/quota-account-summaries` | `tenant:quota-read`, platform; `page`, `pageSize`, optional `tenantId`, `quotaCode` |
| GET `/api/v1/admin/gpu/tenants` | `tenant:quota-read`, platform; `page`, `pageSize` (default/max6), optional `tenantId`; latest exporter memory and current quota |

For POST query requests the body is `{ "data": { "window": "24h", "metricCodes": [...] } }`. Use `start` and `end` RFC3339 UTC timestamps instead of `window` for a controlled custom range. `clusterId` and `modelKey` are available only on physical platform reads. Latest requests use query parameters. Unknown/duplicate selector fields, public tenant/owner/Pod selectors and ambiguous JSON are rejected. The route's path identities cannot be supplied through the request body.

Default range is 24h; presets are 1h, 6h, 24h, 7d and 30d. Intervals are `[start,end)` within the last 30 days. Obs chooses display buckets independently of the 30s resource/15s physical base grid. Response values are nullable; complete-only mean/peak, partial known sums, original sample-time bounds and quality/coverage remain distinct. Optional event/state metrics expose snapshot semantics and Xid process-start boundaries, never an invented 30-day event total.

Resource details enable `container` (owner `ani-resource-service`) only in this batch. Original create-operation identity comes from the Gov ledger after current resource-read authorization. A live owner object or Pod is not required, and refunded original charges remain queryable. Tenant aggregates query retained labeled Prometheus records, including records for subsequently deleted resources; they do not enumerate current business resources. ModelDev's future detail adapter uses execution ID and original create operation. ModelDev and Inference GPU details remain explicitly unsupported in this phase.

Tenant replies contain business target identity and aggregate quality only. Pod, node, GPU UUID, binding IDs and internal scope watermarks are excluded from the public projection.

## Service calls

| Caller -> receiver | Registered RPC / authority | Deadline |
|---|---|---|
| Gov -> ACC | `AcceleratorAdminService/ListDevices`, existing platform `AdminRead` | Existing bounded ACC timeout |
| Gov -> Obs | `/observability.gpu.v1.GpuMonitoringService/QueryGpuLatest`, `QueryGpuRange` | 32s Gov call, 30s Obs batch, earlier parent honored |

TLS 1.3, unique exact DNS client `ani-governance`, exact registered server DNS, method allowlists and rebuilt metadata apply. GPU Obs calls use `x-ani-context=platform` with no tenant header, or `x-ani-context=tenant` with the authenticated mapped tenant UUID. The target tenant remains explicit for an administrator aggregate. Both contexts carry the real authenticated actor and a request UUID. Legacy CPU/memory/network/training RPC validation, timeouts and source rules remain unchanged. GPU HTTP gets a 35s bound.

Tenant queries pass only a verified tenant UUID; resource queries pass the original full `UsageRef` (tenant, owner service, resource ID and create operation ID) after current resource-read authorization and original ledger identity checks. Business `GpuTarget.scopes` is empty. Physical scopes contain only the authorized cluster/node/device selectors. The retired ACC scope RPCs and scope pagination are removed, without a compatibility mode.

Obs reads the approved native exporter/plugin and KSM records. Business identity uses owner-written labels and retains the complete tenant/owner/resource/create-operation tuple; no ACC Binding or Usage projection is joined. Physical scopes use authorized cluster, node name/UID, adapter, opaque device UID, device kind and parent UID. Missing measurements remain unknown and parent-card/partition aggregation follows the Obs contract.

`indexComplete` describes time coverage of every requested metric, not coverage of every tenant workload. Missing/stale buckets are null; observed zero remains zero. `uncoveredResourceKinds` is always empty. `ownershipAvailableFrom` is deprecated and unfilled. `metricAvailableFrom` is the first visible sample within the requested range. Sample-time bounds are obtained from actual Prometheus record timestamps, not evaluation times.

History begins at the first successful labeled record (T0). Existing unlabeled samples are not backfilled, and Prometheus retention is unchanged. A 30-day query can therefore contain gaps. The deployment operator must record T0 and the existing retention after rollout; source changes do not establish those facts.

Independent GPU processing limits remain 4 metrics, 12 curves, 3000 buckets/curve, 12000 total buckets, 128 unique sources, 12M processed values, <=6h chunks, 4 Prom requests/query, 16 global executing requests, 2 queries/tenant, 10s Prom deadline, 16MiB upstream and 8MiB public response. Business queries use at most 16 configured clusters; platform resource-usage queries may restrict them with their existing trusted `cluster_ids`. Exceeding a limit fails explicitly.


## Quota snapshot and operations

Tenant count/page, quota catalogue, current plan policies and authoritative accounts share one dedicated readonly Repeatable Read transaction with batched queries. Existing admission/refund transactions are unchanged. `configured` comes from policy presence; configured zero and missing policy are distinct. Decimal string `limit`, `occupied`, `available=max(limit-occupied,0)`, `overLimit=occupied>limit` and actual `enforcement` preserve existing numeric behavior. No ACC-derived accounting or historical quota is introduced.

The quota-account summary response also includes `summary`: `configuredTenantCount` counts tenants with at least one selected quota policy; `accounts` contains `quotaCode`, `unit`, `configuredTenantCount`, `limit`, `occupied` and `available` for each selected quota definition. All aggregate numbers are decimal strings, including sums larger than uint64. These totals use the same `tenantId`/`quotaCode` scope and exclude soft-deleted tenants, independently of `page`/`pageSize` (including an empty out-of-range page). Limits of a shared plan are counted once per tenant. Occupation includes accounts without a configured policy. Available is summed after applying `max(limit-occupied,0)` to each tenant. A single PostgreSQL aggregate query runs in the existing read-only snapshot; it does not load all tenant projections into application memory. This is quota accounting, not actual device usage or allocatable ACC capacity.

The combined administrator tenant page uses the same quota snapshot, including its trusted tenant mapping. After the transaction ends it queries the two latest Obs exporter metrics with two workers. Its `observation_basis=EXPORTER_OBSERVED` and `owner_scope=ani-resource-service` describe the observed subset; it does not enumerate ACC bindings or claim complete current allocations. Observed runtime memory/limit bytes and quota accounts remain distinct. Missing observations are null. The page keeps the 12-curve/8MiB GPU budget and a 30s deadline; budget and timeout failures fail explicitly.

API catalogue synchronization remains explicit: first the admin CLI dry-run, then synchronization, then [read-API associations](../../scripts/ops/sql/register-gpu-monitoring-read-apis.sql), then the documented policy refresh. The SQL requires existing enabled read permissions and adds no role grants. Source registration alone does not establish live authorization.

Obs collection manifests in `deploy/gpu/` use one controlled GPU profile per source, the approved official NVIDIA/Hygon exporters and native plugin/KSM facts. They do not deploy another feedback writer. Node UID relabels in the test-environment manifests must be refreshed from verified ACC/Kubernetes identities after node replacement. Old profiles are not used to infer a new node's history. Prometheus retention/capacity is an external dependency; this slice does not modify storage or retention settings.
