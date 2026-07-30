-- Thread 订阅。Member 对单个 Thread 的显式关注,是 thread_activity Item
-- 的路由依据(见 06-inbox-credentials.md 的 Item 类型表)。
-- 订阅不改变 Channel membership,也不授予读取权限:没有 Channel 成员身份的
-- Member 订阅了也读不到 Thread。

CREATE TABLE thread_subscriptions (
    thread_id UUID NOT NULL,
    space_id UUID NOT NULL,
    member_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (thread_id, member_id),
    FOREIGN KEY (thread_id, space_id) REFERENCES threads(id, space_id) ON DELETE RESTRICT,
    FOREIGN KEY (member_id, space_id) REFERENCES members(id, space_id) ON DELETE RESTRICT
);

-- 路由 thread_activity 时按 Thread 取订阅者。
CREATE INDEX thread_subscriptions_by_thread ON thread_subscriptions (thread_id);
