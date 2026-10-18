# Cloudflare 边缘中继方案

这份文档记录把 WebSocket 转发数据面交给 Cloudflare 承载的设计方向。当前仓库的 Go / Gin 中继仍是完整实现，Worker 版本尚未实现，也不应被当作可用部署方案。

## 要解决的问题

Cloudflare 只做 Go 服务前面的 DNS、TLS 或反向代理时，WebSocket 最终仍然连接到 Go 中继。设备的大头数据会经过 Cloudflare，但仍由 Go 进程接收、排队和发送，不能减少源站的转发带宽和连接压力。

要真正卸载转发流量，Worker 必须直接接受客户端的 `wss://` 连接，并把两个客户端连接放进同一个 Durable Object，由 Durable Object 完成会话匹配和数据转发：

```text
设备 A ── WSS ──> Worker ──> 节点 Durable Object <── Worker <── WSS ── 设备 B
                         │
                         └── D1 / Durable Object Storage（设备登记、归属、配额）
```

此时大部分 WebSocket 数据只经过 Cloudflare Worker / Durable Objects，不经过 Go 服务。Go 服务可以继续承担完整控制台、账号管理、审计和高级节点管理。

## 与现有协议的兼容边界

Worker 节点应保持协议版本 1 的公开入口和消息格式：

- `GET /health`
- `POST /v1/register`
- `GET /v1/devices/:id`
- `GET /v1/channel` WebSocket

桌面端已经根据中继根地址把 HTTP 转换为 `ws` / `wss`，因此同一客户端可以连接 Go 节点或 Worker 节点。Worker 只能转发协议消息，不能依赖 Go 的 `net/http`、Gorilla WebSocket、GORM 或 Redis 客户端；它需要使用 TypeScript / JavaScript 的 Workers API 和 WebSocketPair。

## Durable Object 的职责

普通 Worker 的全局变量不能保存可靠的跨请求状态。每个逻辑节点至少需要一个 Durable Object，用于保存在线 WebSocket、会话表、握手阶段、待授权超时、角色互斥、有限队列和带宽令牌桶。当前协议要求的连接方与被连接方互斥、心跳、断线清理和大报文上限都必须在这里实现。

设备登记、归属、禁用状态和账号配额可以放在 D1 或 Durable Object Storage。活动 WebSocket 会话不需要持久化；对象重启后让客户端重新上线，与 Go 中继当前的生命周期约定一致。KV 只适合缓存或目录分发，不应单独承担会话锁、配额扣减或角色互斥。

初版可以采用“一个公共节点一个 Durable Object”的简单分片方式，便于保证账号共享带宽和会话状态的一致性。规模增大后再按设备或账号分片；跨对象路由和共享配额会显著增加实现复杂度。

## 控制面与数据面

推荐先保留 Go 控制面：

1. Go 中继继续提供完整控制台、验证码、账号、设备绑定、批量管理和审计。
2. Worker / Durable Objects 作为独立的边缘数据面，先实现设备注册、WebSocket 握手、授权和加密数据转发。
3. 两边共享版本化的协议测试向量；Worker 节点只有在通过桌面双端互通、角色互斥、重连、限速和大报文测试后，才加入客户端节点目录。

如果之后需要 Worker 独立完成账号和控制台，再增加 D1 事务、管理员认证、验证码、CSRF 和审计，不把 Go 数据库直接暴露给 Worker。

## 部署与成本边界

Worker 可以使用自定义域名提供 HTTPS / WSS，不需要 Nginx，也不能监听任意 TCP 端口。现有 25 秒心跳和 75 秒读超时应保留，以避免长连接被视为空闲。Cloudflare 的 Workers、Durable Objects、存储、长连接和流量额度取决于当前账号计划；免费额度不是无限的，尤其不能把持续的大带宽转发当作零成本服务。

公共准入 Token 仍只是扫描过滤值，不是账号或设备授权。业务帧保持客户端已有的加密和认证边界，Worker 不应记录设备凭据、配对密钥或业务正文。

## 实施顺序

1. 先在本仓库建立 Worker / Durable Objects 原型，不改变 Go 中继和客户端默认节点。
2. 复用协议版本 1 的注册、健康检查、握手、心跳和关闭语义。
3. 用两个桌面客户端验证大流量转发确实不经过 Go 源站，并记录 Cloudflare 实际计费与连接限制。
4. 通过独立节点地址加入客户端目录；Go 节点继续作为功能完整的基准实现和回退节点。
