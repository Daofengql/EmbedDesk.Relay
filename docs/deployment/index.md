# 部署总览与快速开始

这份指南从一台已有 Docker 或 Linux 服务管理环境的主机开始，完成中继启动、HTTPS 接入、首个管理员登录和客户端验证。部署后同一域名提供 `/console/` 控制台、HTTP API 和 WebSocket 中继，无需另开裸 TCP 数据端口。

推荐组合是 **Docker Compose + MySQL + Redis + 现有 Nginx / 宝塔 HTTPS 站点**。Go 程序只监听 HTTP；控制台资源已嵌入程序，运行主机不需要 Node.js 或 .NET。

## 先选一条部署路线

| 你的环境 | 使用步骤 | 对外入口 |
| --- | --- | --- |
| 已有域名、Nginx 或宝塔，允许 Docker | [Compose](docker.md) → [HTTPS 代理](nginx.md) | `https://HOST`，推荐 |
| 已管理 MySQL / Redis，希望用 systemd | [Linux 二进制](binary.md) → [HTTPS 代理](nginx.md) | `https://HOST` |
| 只在可信内网做临时测试 | [Docker 直接映射端口](docker.md#直接暴露-docker-端口) | `http://HOST:PORT` |
| 本机开发、协议回归 | [构建与验证](../development/index.md) | 本机回环地址 |

::: tip 读文档和运行中继是两件事
GitHub Pages 只托管这份静态部署文档；Go 中继、数据库和有状态的 WebSocket 服务需要运行在服务器上。
:::

## 部署前准备

先确认以下信息，再开始编辑配置：

| 项目 | 需要确认的内容 |
| --- | --- |
| 主机架构 | Linux `amd64` 或 `arm64`；二进制必须与主机匹配 |
| 运行环境 | Compose 路线需 Docker Engine 与 `docker compose` 插件；二进制路线需 systemd |
| 域名 | `HOST` 已解析到当前主机；证书覆盖实际使用的主机名 |
| 端口 | 7080 留给宿主回环；公网 HTTPS 使用 443；证书 HTTP 验证按需使用 80 |
| 账号 | 为数据库、Redis 和首个管理员分别准备随机密码 |
| 数据 | 决定备份位置与升级维护时间，不使用已有业务的数据库作为测试库 |

本文所有 `HOST`、`PORT`、`REPLACE_WITH_...` 都是占位值。域名、密码和连接串在主机私有配置中填写，不写入源码或提交记录。

## 快速部署：Compose + HTTPS

以下是完整路线的摘要；第一次部署可直接按 [Compose 分步教程](docker.md)操作。

### 1. 拉取与检查

```sh
docker version
docker compose version
git clone https://github.com/Daofengql/EmbedDesk.Relay.git
cd EmbedDesk.Relay
cp .env.example .env
chmod 600 .env
```

### 2. 修改私有配置

在编辑器中打开 `.env`，替换所有密码占位值，并同步修改对应 DSN / URL。公开地址填 `https://HOST`，不是 `/console/` 地址。

```dotenv
EMBEDDESK_BIND_ADDRESS=127.0.0.1
EMBEDDESK_PORT=7080
EMBEDDESK_PUBLIC_URL=https://HOST
EMBEDDESK_COOKIE_SECURE=true
EMBEDDESK_BOOTSTRAP_USERNAME=admin
EMBEDDESK_BOOTSTRAP_PASSWORD=REPLACE_WITH_RANDOM_ADMIN_PASSWORD
```

首个管理员密码为 12–72 个 UTF-8 字节。NodeSettings 首次初始化后，节点名称、公开地址、公共令牌和默认带宽在控制台修改；启动环境不再覆盖数据库中的值。

### 3. 启动并检查源站

```sh
docker compose config --quiet
docker compose up -d --build
docker compose ps
curl --fail --silent --show-error http://127.0.0.1:7080/ready
```

成功时 `/ready` 返回 HTTP 200，JSON 内容为：

```json
{"protocol":1,"status":"ok"}
```

字段顺序不影响结果。如果连接失败或返回 503，先检查容器和数据库，暂不排查证书。

### 4. 接入 HTTPS

按 [Nginx / 宝塔教程](nginx.md)创建可管理的站点，将域名根路径代理到 `http://127.0.0.1:7080`，配置 WebSocket 和证书。在主机外部检查：

```sh
curl --fail --silent --show-error https://HOST/ready
```

### 5. 首次登录与客户端接入

打开 `https://HOST/console/`，使用私有配置中的 Bootstrap 账号登录。在「中继接入」核对名称、HTTPS 根地址和默认带宽；按需启用公共令牌过滤，复制当前连接代码。

桌面客户端设置 → 中继服务器 → 从剪贴板导入连接代码，选择节点并保存。设置在客户端下次上线时生效，不会迁移现有连接。使用两台客户端进行一次授权与实际转发，才能确认 WSS 代理链路完整。

### 6. 移除初始化凭据

管理员已创建且可以登录后，从 `.env` 删除两项 `EMBEDDESK_BOOTSTRAP_*`，仅重建中继：

```sh
docker compose up -d --no-deps --force-recreate server
```

::: warning 不要用重建数据库来“重置密码”
Bootstrap 只在账号表为空时创建管理员，重启不会改现有密码。不要删除数据库或数据卷来排查登录问题；有数据的部署应由现有管理员管理账号，当前没有自动密码找回入口。
:::

## 上线验收

- [ ] `/health` 和 `/ready` 在源站与 HTTPS 域名均返回 200。
- [ ] 浏览器证书有效，控制台登录与修改节点设置成功。
- [ ] MySQL / Redis 没有映射公网端口，中继 7080 只绑定回环。
- [ ] 两台客户端可以上线、授权，并实际转发一次已发布服务。
- [ ] 默认带宽和单用户带宽符合计划，明确账号下设备共享双向预算。
- [ ] Bootstrap 凭据已移除，数据库与私有配置已有备份。

## 运行边界

一个 Go 进程在内存中维护设备、活动会话和账号共享限速。Redis 存储控制台会话、验证码和尝试限流，不能让多个中继自动组成集群；不要在多个实例前使用随机负载均衡。

重启会结束当前授权和转发，客户端可重新上线，但不会恢复先前设备授权、串口占用或端口连接。运行维护继续阅读[备份与升级](../reference/operations.md)，按症状排查见[常见问题](../reference/faq.md)。
