# Run 不使用租约与 fencing token

Run 原本携带 ownership lease 与 fencing token，Server 定时扫描过期租约并把 Run 判为失败。这套机制解决的是多个候选执行者争抢同一份工作的问题，而 Sumi 不存在这个问题：Trigger 指定 Agent，Agent 归属一台 Computer，该 Computer 上的 Driver 执行，链条每一环都唯一确定。

保留租约还带来一个必然的误判。租约在 Run 创建时开始计时，而 Run 需要经过命令投递、Driver 启动和 Session 打开才进入执行；这段时间没有任何一方有资格续租，定时扫描却覆盖全部非终态 Run，于是正常启动被判为失败。

因此 Run 不再有所有权凭据和期限字段，Server 不再定时改变 Run 状态。失败只由掌握直接证据的 Computer 宣告，重连时双方同步实际状态。Computer 离线是 Computer 的属性，不写入 Run。判定规则见 [Agent Run](../design/04-agent-run.md)。
