# 任务跟踪：本地 Markdown

本仓任务和规格保存在 `.scratch/`。任务跟踪操作直接读写本地文件；本配置不使用 GitHub/GitLab Issues 或 PR 作为任务跟踪器。

## 文件约定

- 每个功能一个目录：`.scratch/<feature-slug>/`。
- 规格：`.scratch/<feature-slug>/spec.md`；既有权威合同以路径引用，不复制第二份。
- 实现票据：`.scratch/<feature-slug>/issues/<NN>-<slug>.md`，从 `01` 编号，一项票据一个文件。
- 普通票据的分诊角色写在顶部 `Status:` 字段，取值见 [标签映射](triage-labels.md)。已分诊票据另有一个 `Category: bug` 或 `Category: enhancement`。
- 评论和对话记录追加到票据底部的 `## Comments`。
- `.scratch/` 是本地过程材料，遵守 [仓库维护规范](../contributing/repository-hygiene.md)，默认不纳入 Git 提交。长期合同和已接受决策保存在正式文档中。

## 技能调用与完成语义

- 技能要求“发布到任务跟踪器”时，在对应 `.scratch/<feature-slug>/` 中创建文件；实现票据遵守上述逐项文件约定。
- 技能要求“读取相关票据”时，读取用户指定路径或对应编号文件。
- `Status: ready-for-agent` 只表示任务准备程度，不代表已完成，也不扩大实施、提交、推送或部署授权。
- 普通票据另用 `Resolution: open`、`resolved`、`wontfix` 记录生命周期，初始为 `open`。它与分诊 `Status:` 分开，完成票据不新增第六个分诊角色。
- 实际完成该票据要求并通过其必要验收后，将变更、具名验证结果、证据链接和未验证范围写入 `## Result`，再设 `Resolution: resolved`；不能用计划、文件存在、skip 或范围内未完成验收替代完成。
- 决定不处理时写明原因，设 `Status: wontfix` 和 `Resolution: wontfix`。关闭不处理不代表交付了该能力。
- 普通实现票据的依赖写为 `Blocked by: NN, NN`，编号属于同一功能目录。只有所列票据的 `Resolution` 均为 `resolved` 时才解除依赖；`wontfix` 不满足实现依赖，需重新确定范围或依赖。
- 技能要求“关闭/解决票据”时，执行上述本地文件更新；提交或合并代码不自动关闭票据。父规格只在其完整范围实际完成后单独结项。

## Wayfinding operations

Wayfinder 负责探索决策；地图与决策票据按本节记录，不参加普通票据分诊，不使用五个 triage 标签，也不写普通票据的 `Resolution:`。

- 地图：`.scratch/<effort>/map.md`，保存 `Destination`、`Notes`、`Decisions so far`、`Not yet specified`、`Out of scope`；`Labels: wayfinder:map` 标识地图。
- 子票据：`.scratch/<effort>/issues/NN-<slug>.md`，从 `01` 编号。`Type:` 为 `research`、`prototype`、`grilling` 或 `task`，对应唯一 `Labels: wayfinder:<type>`，问题写在 `## Question`。
- 生命周期使用 `Status: open`、`claimed`、`resolved`。它与普通票据的分诊 `Status:` 属于不同票据类型，不混用。
- 阻塞关系：票据顶部写 `Blocked by: NN, NN`；所有阻塞票据为 `Status: resolved` 时才可推进。
- 可执行票据：按编号选择 `open` 且没有未解决阻塞项的票据；`claimed` 票据不重复领取。
- 领取：先重读票据，写入 `Status: claimed` 和 `Claimed by:` 领取者标识并保存，再开展工作；并行委派遵守 AGENTS 的职责与文件所有权约束。
- 解决：将结论追加到 `## Answer`，写入 `Status: resolved`，并在地图的 `Decisions so far` 中记录摘要和以票据标题为名称的链接。决策解决不等于业务实现已验收。
