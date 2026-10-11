# Triage 标签

工程技能使用五个标准分诊角色。普通分诊/实现票据的 `Status:` 使用下表取值；完成与关闭使用 [任务跟踪配置](issue-tracker.md) 中的 `Resolution:`，不增加或混用分诊角色。

| 技能角色 | 本仓标签 | 含义 |
|---|---|---|
| `needs-triage` | `needs-triage` | 等待维护者评估 |
| `needs-info` | `needs-info` | 等待报告者补充信息 |
| `ready-for-agent` | `ready-for-agent` | 需求已明确，可交给 agent 实施 |
| `ready-for-human` | `ready-for-human` | 需要人工实施 |
| `wontfix` | `wontfix` | 已决定不处理 |

技能要求应用某个分诊角色时，更新普通票据的 `Status:`。已分诊票据另用 `Category:` 记录 `bug` 或 `enhancement`；每张票据保持一个类别和一个分诊角色。

Wayfinder 地图与决策票据只使用 `wayfinder:*` 标签及其专用生命周期，不使用本表的分诊角色。
