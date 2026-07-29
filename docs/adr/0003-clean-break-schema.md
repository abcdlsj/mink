# 新版本采用断代 schema

新领域模型会替换 Task、Run、Inbox 和 Driver Session 的旧关系。项目只维护新 schema，不迁移旧数据，也不提供兼容读取、双写、deprecated 字段或旧 API。保留兼容层会让旧模型继续约束模块边界，并使新设计无法成为唯一事实。
