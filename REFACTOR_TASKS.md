# 基础重构任务追踪

> 目标：在不改变产品事实、权限合同、协议和 SQLite 事务语义的前提下，收窄 API 边界、明确 bounded context、整理 SQL 所有权并改善可读性。

## 约束

- [x] 不把 `computer/state` 本地 SQLite 与 Server Store 合并
- [x] 不引入 ORM、通用 Repository、CQRS 框架或万能事务封装
- [x] 每个任务完成后运行与改动匹配的 format、lint、test、build
- [x] 不删除权限、事务、replay、fence、恢复相关高价值测试
- [x] 领域语义变化先同步 `DESIGN.md`（本轮未改变领域语义）

## 阶段一：边界与 API

- [x] 盘点并冻结 bounded context 与事实所有权（已记录于 `DESIGN.md`）
- [x] 为 Agent、Computer、Organization、Grant、Audit、Placement、Collaboration Service 定义窄接口
- [x] 将 Service 的输入从完整 `*store.Store` 迁移到调用方接口
- [ ] 将 transport 依赖的 Params、Entity、Error 从 `store` 逐步迁入所属上下文
- [ ] 统一 mutation/read 请求元数据命名
- [x] 将 `GetRun`、`HeartbeatComputer`、Computer placement read、`CheckPermission`、`ListAuditEvents` 的多参数签名收敛为 Params
- [ ] 统一 mutation replay、receipt、committed-at 返回合同
- [ ] 在 proto 不变的前提下统一列表排序、分页和 cursor 约定
- [x] 收口领域状态、主体类型、作用域类型和 capability 类型

## 阶段二：SQLite 所有权

- [x] 保留单一 Server SQLite 连接、迁移序列和显式事务边界
- [x] 为 Authority、Collaboration、Work、Execution、Computer、Artifact、Knowledge 标记 SQL 所有者
- [x] 将 query、scan、authorization、projection、replay helper 按上下文归组（保持单 Store package，按上下文与真实职责文件族归组）
- [x] 删除动态表名等不必要的通用 SQL helper
- [x] 明确跨上下文事务的编排者，不用隐式跨域调用掩盖边界（由显式 Store application command 编排）
- [x] 保持 Artifact blob 和 Computer State 的独立存储边界

## 阶段三：可读性与状态机

- [x] 拆分 `CompleteRun` 的校验、授权、回放、变更、receipt 阶段
- [x] 拆分 `ResolveHeldDraft` 的 cancel/retry/retarget 分支
- [x] 将 `changeMember(..., add bool)` 改为显式 typed change
- [x] 将 `changeSpaceArchive(..., archive bool)` 改为显式 typed change
- [x] 将 Delivery/Run/Launch 组合不变量从 transport mapping 中收口
- [x] 将 Collaboration、Computer、Grant、Placement、Delivery 长 Service 文件拆为 handler、request、response、error 文件

## 阶段四：Web 与验证

- [x] 集中 Web Connect transport 和 client 创建
- [x] 拆分 Web collaboration query、command、pagination、permission、error 模块
- [ ] 按上下文整理测试 fixture，保留独立诊断价值的行为测试
- [x] 运行 format、generate、lint、test、race、build
- [x] 更新 `DESIGN.md`、本任务清单和工作区状态

## 阶段五：真实 DDD 收益（本轮）

- [x] Execution：将 Delivery/Run/Launch 组合不变量和状态转换提取为可脱离 SQLite 的领域决策
- [x] Collaboration：将 Space/Membership/Message 的生命周期和 mutation 规则提取为可独立测试的领域决策
- [x] Application API：Collaboration mutation 与 Delivery transport 使用语义化 command/result，不再直接暴露 `store.*Params` 或 scan entity
- [x] Typed state：Execution 状态以及 Collaboration principal、Space、target 类型集中在领域边界
- [x] 补充 Execution、Collaboration 领域不变量测试，并保留 application/SQLite 的权限、replay、Audit、事务行为测试
- [x] 随 Authority 上下文改造收口 principal、scope 与 capability 类型，Store 只保留迁移兼容别名

## 阶段六：bounded context 目录布局（本轮）

- [x] 将拼接式顶层包名移动到所属上下文下的清晰子目录（`computer/app`、`computer/host`、`computer/state`、`execution/*`、`transport/*` 等）
- [x] 用语义化 import alias 保持调用方可读性，避免 `app`、`state`、`domain` 等泛化包名在跨上下文代码中失去语义
- [x] 清理旧目录和旧 import path；目录重组不改变产品事实、proto、SQLite schema、事务或权限合同
- [x] 将目录布局决策同步到 `DESIGN.md`

## 阶段七：Authority 与 Grant API（本轮）

- [x] 将 Principal、Scope、Capability 定义迁入 `authority/domain`，使用 typed kind 与 capability
- [x] 将 Grant fact、Issue/Revoke command 和 Get/List/Permission query 迁入 `grant/application`
- [x] Grant transport、request/response mapping、error mapping 与 persistence port 不再依赖 `store`
- [x] Authority Human/Browser、runtime identity 与 browser session adapter 直接依赖 Authority Principal/error
- [x] Store 保持 SQLite、transaction、replay、Audit 和 grant chain 所有权，只保留调用方迁移所需的类型与错误别名
- [x] 用 Authority 领域规则测试和完整 proto capability round-trip 测试替换单点 capability mapping 测试
- [x] 完成 format、generate、lint、Go/Web test、race、build 与 Store 边界扫描
