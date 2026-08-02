# Context Citation 只记录 Agent 声明的公开证据

Sumi 将回答与上下文的可见关系建模为 Agent 随 Message 提交的 Context Citation，并由 Server 验证来源已经进入当前 Run。系统不从 Provider attention、transcript 或文本相似度推断因果依赖，因为这些信号不可稳定取得，也不能向 Human 证明模型为何生成某句话；该选择允许 UI 展示可追溯来源，同时明确引用不等于隐藏推理或事实正确性。
