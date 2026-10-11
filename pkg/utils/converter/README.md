# 权限码转换器

本包提供菜单与 API 两种权限码转换器。转换算法生成登记用的权限码；实际访问仍经过认证、租户闸门和授权。

## 菜单转换

[MenuPermissionConverter](menu.go) 的 `ConvertCode(path, title, typ)` 先去除路径首尾空白和 `/`。路径有多个段时，先丢弃第一个段，再对剩余段去除空白并用 `inflection.Singular` 单数化，过滤空段及以 `:` 开头的段，最后用 `:` 连接。路径本身没有统一转小写或 kebab-case 的步骤。

| Menu 类型 | 动作 |
|---|---|
| `CATALOG` | `dir` |
| `MENU`、`EMBEDDED` | `view` |
| `BUTTON` | 按标题匹配，见下文 |
| `LINK` | `jump` |
| 未知类型 | 不加动作后缀 |

按钮标题去除首尾空白、转小写后，按 `create` → `edit` → `delete` → `import` → `export` 的顺序匹配中英文关键词。匹配先检查 token 的精确或前缀命中，再检查整句包含；空标题或无匹配返回 `act`。例如 `add/create/new/新增/添加/创建` 返回 `create`。

| 输入 | 结果 |
|---|---|
| `/users`，`BUTTON`，`新增` | `user:create` |
| `/admin/settings`，`MENU` | `setting:view` |
| `/reports`，`BUTTON`，`一键导出为Excel` | `report:export` |

空路径直接返回空字符串。若剩余路径段全部被过滤而类型有动作，结果会只有动作前缀，例如 `a/:b/:c` 配合普通按钮标题返回 `:act`。

`ComposeMenuPaths` 按父子关系就地写回菜单 `Path`，再由调用方决定是否转换权限码；缺失父节点、自引用和循环的处理见实现与路径测试。

## API 转换

[ApiPermissionConverter](api.go) 提供两个入口：

- `ConvertCodeByPath(method, path)` 去掉规则允许的 API/版本前缀和 `{参数}` 段，只取剩余第一个资源段，单数化并去掉该段冒号后的部分。GET 映射 `view`，POST 映射 `create`，PUT/PATCH 映射 `edit`，DELETE 映射 `delete`；原路径以 `/list` 结尾时动作是 `view`，其他方法使用小写方法名。例如 `GET /api/v1/admin/settings` 返回 `admin:view`。
- `ConvertCodeByOperationID` 解析 `Service_Method` 形式，按 service、方法动作及可选名称生成权限码，归一化查询/创建/更新/删除动作。例如 `TaskService_ListTaskTypeName` 返回 `task:task-type-name:view`；不符合解析格式返回空字符串。

菜单转换与 API 转换的路径规则分别维护，不能用一个入口的示例推导另一个入口的结果。

## 验证与来源

现有测试是 [menu_test.go](menu_test.go)、[menu_edge_test.go](menu_edge_test.go)、[menu_paths_test.go](menu_paths_test.go)、[api_test.go](api_test.go) 和 [api_edge_test.go](api_edge_test.go)。在仓库根目录使用 `go test ./pkg/utils/converter` 定向验证。

项目来源与原许可见 [THIRD_PARTY_NOTICES.md](../../../THIRD_PARTY_NOTICES.md)。
