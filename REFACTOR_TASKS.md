# 基础重构任务追踪

> 目标：在不改变产品事实、权限合同、协议和 SQLite 事务语义的前提下，收窄 API 边界、明确 bounded context、整理 SQL 所有权并改善可读性。

## 约束

- [x] 不把 `computerstate` 本地 SQLite 与 Server Store 合并
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
- [ ] 收口领域状态、主体类型、作用域类型和 capability 类型

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
