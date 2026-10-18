# Docker Compose 部署

本教程使用仓库自带 [compose.yml](../../compose.yml)，从源码构建中继，同时启动独立 MySQL 8.4 与 Redis 7.4。默认仅把中继 HTTP 映射到宿主 `127.0.0.1:7080`，随后接入已有 HTTPS 站点。

## 1. 检查环境与工作目录

在 Linux 主机执行：

```sh
docker version
docker compose version
git clone https://github.com/Daofengql/EmbedDesk.Relay.git
cd EmbedDesk.Relay
```

两条版本命令都应成功。下文命令均在这个 Compose 项目目录运行；换目录时需明确 `-f` 文件路径与对应环境文件。

| 服务 | Compose 内地址 | 宿主默认暴露 | 数据位置 |
| --- | --- | --- | --- |
| `server` | `server:7080` | `127.0.0.1:7080` | 账号等数据写入 MySQL |
| `mysql` | `mysql:3306` | 不映射 | 命名卷 `mysql-data` |
| `redis` | `redis:6379` | 不映射 | 命名卷 `redis-data`，启用 AOF |

中继镜像使用非 root 用户。Go 与 Node.js 只在镜像构建阶段使用，不需要单独安装到运行主机。

## 2. 准备 `.env`

```sh
cp .env.example .env
chmod 600 .env
```

使用私有编辑器修改，不把真实密码粘贴进公开终端记录或 Git。下面是一份首次公网部署的配置结构；必须替换每个占位值：

```dotenv
# 三类密码分别生成；同一密码在连接串中的副本必须保持一致。
MYSQL_ROOT_PASSWORD=REPLACE_WITH_RANDOM_ROOT_PASSWORD
MYSQL_PASSWORD=REPLACE_WITH_RANDOM_DATABASE_PASSWORD
REDIS_PASSWORD=REPLACE_WITH_RANDOM_REDIS_PASSWORD
EMBEDDESK_MYSQL_DSN=embeddesk:REPLACE_WITH_RANDOM_DATABASE_PASSWORD@tcp(mysql:3306)/embeddesk?charset=utf8mb4
EMBEDDESK_REDIS_URL=redis://:REPLACE_WITH_RANDOM_REDIS_PASSWORD@redis:6379/0

# 宿主只开放回环 HTTP，域名通过已有 Nginx / 宝塔提供 HTTPS。
EMBEDDESK_BIND_ADDRESS=127.0.0.1
EMBEDDESK_PORT=7080
EMBEDDESK_PUBLIC_URL=https://HOST
EMBEDDESK_COOKIE_SECURE=true

# 仅在首次初始化时使用；之后在控制台管理节点设置。
EMBEDDESK_RELAY_NAME=EmbedDesk
EMBEDDESK_ADMISSION_KEY=
EMBEDDESK_BANDWIDTH_BITS_PER_SECOND=10000000
EMBEDDESK_REGISTRATION_ENABLED=false

# 空账号表创建首个管理员，成功后删除这两行。
EMBEDDESK_BOOTSTRAP_USERNAME=admin
EMBEDDESK_BOOTSTRAP_PASSWORD=REPLACE_WITH_RANDOM_ADMIN_PASSWORD
```

`10000000` 是 10 Mbps。它是每台匿名设备的默认带宽，也是新注册账号总带宽的初始值，不是绑定设备各自获得的独立额度。

### 密码与连接串如何对应

| 要修改的值 | 必须同步的位置 |
| --- | --- |
| `MYSQL_PASSWORD` | `EMBEDDESK_MYSQL_DSN` 中 `embeddesk:` 后的密码 |
| `REDIS_PASSWORD` | `EMBEDDESK_REDIS_URL` 中 `redis://:` 后的密码 |
| `MYSQL_ROOT_PASSWORD` | 仅 MySQL 管理账号，不放入中继 DSN |
| Bootstrap 密码 | 独立于数据库和 Redis 密码，用于网页首次登录 |

::: tip 首次部署减少连接串编码问题
可以使用密码管理器生成足够长的随机字母数字密码。包含特殊字符时，Redis 密码须按 URL 语法编码，MySQL 按 DSN 语法处理；`.env` 中的 `$` 还涉及 Compose 插值。不要假设三种语法完全一样。检查配置使用 `docker compose config --quiet`，不要把完整展开配置贴到公开日志，它会包含密码。
:::

## 3. 构建并启动

```sh
docker compose config --quiet
docker compose up -d --build
docker compose ps
```

`config --quiet` 成功时没有输出。首次构建需要下载镜像、Go 与前端依赖。MySQL 和 Redis 完成健康检查后，Compose 才启动 `server`。

查看状态时，MySQL / Redis 应为 `healthy`，中继应为 `Up`。当前中继容器本身没有 Docker `HEALTHCHECK`，因此还必须请求 `/ready`：

```sh
curl --fail --silent --show-error http://127.0.0.1:7080/health
curl --fail --silent --show-error http://127.0.0.1:7080/ready
```

两者正常时都是 HTTP 200，内容为：

```json
{"protocol":1,"status":"ok"}
```

`/health` 只证明进程响应；`/ready` 会检查数据库和已配置 Redis，异常时返回 HTTP 503 与 `{"status":"unavailable"}`。

::: details 没有启动成功时先看这里

```sh
docker compose ps -a
docker compose logs --tail=100 server
docker compose logs --tail=100 mysql redis
```

- `server` 不断重启：先核对 DSN、Redis URL、Bootstrap 密码长度与管理员初始化条件。
- `mysql` 不健康：确认密码不再是占位值，磁盘和数据目录可用；已有数据卷不能只改 `.env` 就修改数据库密码。
- 宿主 7080 端口已占用：检查 `ss -ltnp`，调整宿主 `EMBEDDESK_PORT`，并同步 Nginx 上游端口。

更多现象见[故障排查](../reference/faq.md)。不要通过删除数据卷来试错。
:::

## 4. 配置 HTTPS 并登录

按 [Nginx / 宝塔步骤](nginx.md)把 `https://HOST/` 代理到 `http://127.0.0.1:7080`。使用域名访问：

```sh
curl --fail --silent --show-error https://HOST/ready
```

然后打开 `https://HOST/console/`，输入 `.env` 里设置的 Bootstrap 账号密码。不要使用公网 HTTP 登录；当前配置的 Secure Cookie 只在 HTTPS 下发送。

进入「中继接入」核对公开地址、默认带宽，按需要开启公共令牌过滤。将当前连接代码导入两台桌面客户端，验证上线、授权和转发。只有页面能打开，尚不能证明 WebSocket 配置正确。

## 5. 完成初始化收尾

首个管理员创建且可登录后，删除 `.env` 中两行 `EMBEDDESK_BOOTSTRAP_*`，执行：

```sh
docker compose up -d --no-deps --force-recreate server
curl --fail --silent --show-error http://127.0.0.1:7080/ready
```

`--no-deps` 只重建中继，不重启 MySQL / Redis。此时已有设备会断线并重新上线，活动授权和转发不会自动恢复。

Bootstrap 仅创建空账号表，不重置已有密码。以后调整名称、公开地址、公共令牌与默认带宽，在控制台保存即可；注册开关仍来自 `.env`，修改后需重建中继。

## 直接暴露 Docker 端口

可信内网临时测试可以不用 Nginx。先选择未占用的宿主端口，将以下项修改为实际值：

```dotenv
EMBEDDESK_BIND_ADDRESS=0.0.0.0
EMBEDDESK_PORT=PORT
EMBEDDESK_PUBLIC_URL=http://HOST:PORT
EMBEDDESK_COOKIE_SECURE=false
```

重建中继后访问 `http://HOST:PORT/console/`，防火墙只开放所需端口。容器内部仍监听 7080，不需要改 Dockerfile。此段适合新建的临时测试节点；已有 HTTP 节点换地址时，先从原控制台保存新公开地址，再切换访问入口。已有 HTTPS 生产节点应另建隔离测试实例，不按此流程直接降级。

::: warning HTTP 只能用于可信环境
HTTP / WS 会明文传输登录链路、设备凭据和控制消息；业务正文既有加密不能替代 HTTPS。改回公网 HTTPS 时，先将 `EMBEDDESK_COOKIE_SECURE=true` 并重建中继，再在控制台保存 HTTPS 公开地址。
:::

## 使用预构建二进制镜像

主机资源较少时，可在开发机[构建相应 Linux 架构](binary.md#构建程序)，再复制二进制和 [Dockerfile.binary](../../deploy/Dockerfile.binary) 到独立构建目录：

```text
binary-image/
├── embeddesk-server
└── Dockerfile.binary
```

在这个目录构建：

```sh
docker build -f Dockerfile.binary -t embeddesk-relay:local .
```

镜像需在部署主机本地存在，或经自己的镜像分发流程导入。在 Compose 的 `.env` 设置同名 `EMBEDDESK_IMAGE=embeddesk-relay:local`，回到项目目录执行：

```sh
docker compose up -d --no-build
```

不要再传 `--build`，否则会按仓库 Dockerfile 重新构建。这个二进制镜像同样需要 Compose 中的 MySQL / Redis 配置，不会自动携带数据库。

## 日常管理

| 目的 | 命令 | 数据影响 |
| --- | --- | --- |
| 查看日志 | `docker compose logs --tail=100 server` | 只读 |
| 暂停中继 | `docker compose stop server` | 保留数据库与卷 |
| 启动现有中继 | `docker compose start server` | 不应用新环境配置 |
| 应用 `.env` 变更 | `docker compose up -d --no-deps --force-recreate server` | 重建中继，活动会话结束 |
| 停止整套服务 | `docker compose down` | 保留命名卷 |

::: danger 不要把删除卷当成重启
`docker compose down -v` 会删除命名卷及数据库数据。日常升级、端口调整、密码排查都不需要这个命令。先按[备份与恢复流程](../reference/operations.md)保留可恢复数据。
:::
