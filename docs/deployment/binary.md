# Linux 单二进制与 systemd

这条路线适合已管理 MySQL / Redis、希望直接用 systemd 运行 Go 服务的 Linux 主机。二进制内嵌 React 控制台、图标和许可证；运行时无需 Node.js、前端文件夹或 .NET。

## 构建程序

构建机需要 Go 1.26.9+、Node.js 22.12+（建议 24）和 npm。在仓库根执行完整脚本：

```powershell
./build.ps1
```

默认生成 `artifacts/linux-amd64/embeddesk-server`。如果部署主机为 ARM64：

```powershell
./build.ps1 -TargetOS linux -TargetArch arm64
```

Linux / macOS 构建：

```sh
sh build.sh
```

或选择 ARM64：

```sh
GOOS=linux GOARCH=arm64 sh build.sh
```

在部署主机运行 `uname -m`，`x86_64` 对应 `amd64`，`aarch64` 对应 `arm64`。把对应二进制和仓库的 `deploy/embeddesk-server.service` 上传到主机的临时工作目录，下文安装命令在该目录执行。

::: warning 完整构建才包含控制台
脚本先执行前端构建和许可收集，再生成 Go 二进制。只运行 `go build ./cmd/relay` 不保证 `www/dist` 已有控制台；缺少资源时 `/console/` 返回 503。不要把构建机源码目录当作运行依赖补到服务旁。
:::

## 1. 准备专用账号与目录

先检查账号是否存在：

```sh
id embeddesk
```

不存在时创建，已存在则确认是该服务的专用账号后复用：

```sh
sudo useradd --system --home-dir /var/lib/embeddesk --shell /usr/sbin/nologin embeddesk
```

创建目录和安装程序：

```sh
sudo install -d -o root -g root -m 755 /opt/embeddesk
sudo install -d -o root -g embeddesk -m 750 /etc/embeddesk
sudo install -d -o embeddesk -g embeddesk -m 700 /var/lib/embeddesk
sudo install -o root -g root -m 755 embeddesk-server /opt/embeddesk/embeddesk-server
sudo install -o root -g embeddesk -m 640 /dev/null /etc/embeddesk/server.env
```

| 路径 | 所有权 / 权限 | 用途 |
| --- | --- | --- |
| `/opt/embeddesk/embeddesk-server` | root，755 | 只读程序，由管理员更新 |
| `/etc/embeddesk/server.env` | root:embeddesk，640 | 私有环境配置，不进入 Git |
| `/var/lib/embeddesk` | embeddesk，700 | 服务工作目录，SQLite / 运行数据 |

后续修改已存在环境文件时直接编辑，不重复执行 `/dev/null` 的安装命令，否则会清空原配置。

## 2. 准备数据库与 Redis

生产推荐为中继创建独立 MySQL 数据库与数据库账号。通过现有数据库管理工具，以管理员身份建立 `utf8mb4` 数据库，并仅授权该库；不要给服务使用 MySQL root。

::: details 本机 MySQL 的建库结构示例
以下 SQL 在数据库管理员会话中执行，密码占位值必须替换。这里的账号只允许本机 TCP 地址；远程数据库应按实际来源设置授权与 TLS。

```sql
CREATE DATABASE embeddesk CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE USER 'embeddesk'@'127.0.0.1' IDENTIFIED BY 'REPLACE_WITH_RANDOM_DATABASE_PASSWORD';
GRANT ALL PRIVILEGES ON embeddesk.* TO 'embeddesk'@'127.0.0.1';
```

程序启动时执行 GORM 增量迁移，需要该专用库内的建表 / 修改表权限。已经创建数据库或账号时不要重复运行创建语句，应通过既有管理流程核对授权。
:::

Redis 使用已有受保护的独立实例或逻辑隔离前缀。它存储控制台会话、验证码和限流数据，不保存设备业务正文。不要为了让中继连接而把 Redis / MySQL 直接开放到公网。

## 3. 编辑初始环境

使用私有编辑器：

```sh
sudoedit /etc/embeddesk/server.env
```

公网 HTTPS 的起始配置如下，替换全部占位值：

```dotenv
EMBEDDESK_LISTEN=127.0.0.1:7080
EMBEDDESK_DATABASE_DRIVER=mysql
EMBEDDESK_MYSQL_DSN=embeddesk:REPLACE_WITH_RANDOM_DATABASE_PASSWORD@tcp(127.0.0.1:3306)/embeddesk?charset=utf8mb4
EMBEDDESK_REDIS_URL=redis://:REPLACE_WITH_RANDOM_REDIS_PASSWORD@127.0.0.1:6379/0
EMBEDDESK_REDIS_PREFIX=embeddesk:
EMBEDDESK_CONSOLE_ENABLED=true
EMBEDDESK_REGISTRATION_ENABLED=false
EMBEDDESK_COOKIE_SECURE=true
EMBEDDESK_PUBLIC_URL=https://HOST
EMBEDDESK_RELAY_NAME=EmbedDesk
EMBEDDESK_BANDWIDTH_BITS_PER_SECOND=10000000
EMBEDDESK_BOOTSTRAP_USERNAME=admin
EMBEDDESK_BOOTSTRAP_PASSWORD=REPLACE_WITH_RANDOM_ADMIN_PASSWORD
```

管理员密码为 12–72 个 UTF-8 字节。环境文件不是 shell 脚本，不加入 `export`，不依赖变量展开或命令替换。连接串需要按 MySQL DSN / Redis URL 语法填写，详细规则见[配置参考](../reference/configuration.md)。

### 使用 SQLite 的单机配置

如果只是单机开发验证，可替换数据库项并删除 MySQL DSN：

```dotenv
EMBEDDESK_DATABASE_DRIVER=sqlite
EMBEDDESK_DATABASE=/var/lib/embeddesk/relay.sqlite
```

不使用 Redis 时也删除 `EMBEDDESK_REDIS_URL`，会话和验证码会保存到数据库。SQLite 与 MySQL 不会自动互导；不要把 SQLite 文件路径写到只读的 `/opt` 或 `/etc`。

## 4. 安装并启动 systemd

安装上传的服务示例：

```sh
sudo install -o root -g root -m 644 embeddesk-server.service /etc/systemd/system/embeddesk-server.service
sudo systemctl daemon-reload
sudo systemctl enable --now embeddesk-server
sudo systemctl status embeddesk-server --no-pager
```

预期服务为 `active (running)`。日志中会出现协议版本、HTTP-only 与控制台启用状态；不能仅凭进程存在认定数据库就绪，继续执行：

```sh
curl --fail --silent --show-error http://127.0.0.1:7080/health
curl --fail --silent --show-error http://127.0.0.1:7080/ready
```

正常为 HTTP 200：

```json
{"protocol":1,"status":"ok"}
```

::: details 服务示例中的限制有什么影响
[embeddesk-server.service](../../deploy/embeddesk-server.service) 使用 `User=embeddesk`、`ProtectSystem=strict`、`ProtectHome=true`、`PrivateTmp=true`，仅允许写 `/var/lib/embeddesk`。这意味着：

- SQLite 和其他运行数据应放到该目录，不放在管理员家目录。
- 更换数据路径时同时修改目录权限和 `ReadWritePaths`。
- 更换程序或环境路径时同步修改 `ExecStart` / `EnvironmentFile`。
- 变更 service 单元后执行 `daemon-reload`，只改环境文件则重启服务即可。
:::

## 5. 接入 HTTPS 与完成初始化

按 [Nginx / 宝塔教程](nginx.md)配置站点与证书，访问 `https://HOST/console/` 登录。核对公开地址、带宽和准入过滤，导入客户端并验证实际转发。

创建管理员成功后，再次编辑环境文件，删除两项 Bootstrap 值：

```sh
sudoedit /etc/embeddesk/server.env
sudo systemctl restart embeddesk-server
curl --fail --silent --show-error http://127.0.0.1:7080/ready
```

重启会结束活动设备授权和转发。环境文件仍需要保留数据库和 Redis 凭据，权限不可改成所有用户可读。

## 日常排查和更新

```sh
sudo journalctl -u embeddesk-server -n 100 --no-pager
sudo systemctl is-active embeddesk-server
sudo ss -ltnp
```

| 现象 | 检查方向 |
| --- | --- |
| `Exec format error` | 二进制架构是否匹配 `uname -m` |
| 服务启动后立即失败 | 查看 journal，核对环境语法、数据库 / Redis、Bootstrap 条件 |
| SQLite 权限错误 | 数据目录所有者和 `ReadWritePaths` |
| `HTTP listener failed` | 7080 是否占用，监听地址是否存在 |
| 本机就绪成功、外部 502 | Nginx 目标地址和端口 |

升级前备份数据库、环境文件和旧二进制。在维护时段停止服务，安装新文件后启动，完成 `/ready` 和客户端转发检查。具体命令与数据库回滚边界见[备份、恢复与升级](../reference/operations.md)。
