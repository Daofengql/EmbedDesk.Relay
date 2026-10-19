# 健康检查、备份与升级

运维按三个层次检查：进程是否响应、存储是否就绪、客户端是否实际完成授权与转发。升级前保留数据库、私有配置与旧程序，不能只保留一个容器标签。

## 健康检查的预期结果

```sh
curl --fail --silent --show-error http://127.0.0.1:7080/health
curl --fail --silent --show-error http://127.0.0.1:7080/ready
curl --fail --silent --show-error https://HOST/ready
```

| 入口 / 检查 | 正常 | 异常说明 |
| --- | --- | --- |
| `/health` | HTTP 200，`{"protocol":1,"status":"ok"}` | 进程 / 端口 / 代理不可达 |
| `/ready` | 同上 | 存储异常时 HTTP 503，`{"status":"unavailable"}` |
| 登录 / 验证码 | 控制台能登录、注册开关符合预期 | Cookie、Origin、会话或验证码存储问题 |
| 双客户端授权与转发 | 实际服务打开、收发正常 | 需继续检查 WSS、准入、设备策略和客户端目标 |

`/ready` 在最多约 2 秒的检查上下文中测试数据库与已配置 Redis。当前没有自动修复、历史监控或集群健康仲裁；可以把该 HTTP 检查接入自己的监控系统。

## 日志与日常巡检

Compose：

```sh
docker compose ps -a
docker compose logs --tail=100 server
docker compose logs --tail=100 mysql redis
```

systemd：

```sh
sudo systemctl status embeddesk-server --no-pager
sudo journalctl -u embeddesk-server -n 100 --no-pager
```

Compose 每个服务配置 `json-file` 日志轮转，10 MiB × 3 个文件；systemd 沿用主机 journald 策略。定期检查主机磁盘、MySQL / Redis 卷空间、证书续期与备份恢复演练。

::: warning 提交排查信息前裁剪
不要公开 `.env`、完整 Docker inspect / Compose 展开配置、Cookie、数据库导出或私有设备清单。服务避免记录凭据和业务正文，运维命令的输出仍可能包含主机与路径信息。
:::

## 需要备份什么

| 内容 | 为什么保留 |
| --- | --- |
| MySQL 或 SQLite 一致性备份 | 用户、密码摘要、归属、节点设置和审计 |
| 私有 `.env` / `server.env` | 存储连接、Cookie、注册开关与运行配置 |
| Compose / systemd / Nginx 配置 | 恢复运行方式与代理链路 |
| 当前 Git revision、旧镜像或二进制 | 与备份数据库匹配的回滚版本 |
| Redis 状态（按部署需求） | 控制台会话、验证码、限流；丢失通常需要重新登录 |

数据库保存节点公共令牌与密码摘要，应按敏感数据加密离机备份。原始设备凭据、设备连接密码、配对 / 会话密钥与转发业务正文不落盘，备份不能恢复运行中的授权和转发连接。

## MySQL 备份：Compose 示例

在 Compose 项目目录执行，使用时间戳创建私有目录：

```sh
umask 077
relay_backup_dir="backups/$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -p "$relay_backup_dir"
cp .env compose.yml "$relay_backup_dir/"
git rev-parse HEAD > "$relay_backup_dir/revision.txt"
if docker compose exec -T mysql sh -c 'MYSQL_PWD="$MYSQL_PASSWORD" exec mysqldump --single-transaction --no-tablespaces --set-gtid-purged=OFF -u embeddesk embeddesk' > "$relay_backup_dir/relay.sql.partial" \
  && test -s "$relay_backup_dir/relay.sql.partial"; then
  mv "$relay_backup_dir/relay.sql.partial" "$relay_backup_dir/relay.sql"
  sha256sum "$relay_backup_dir/relay.sql" > "$relay_backup_dir/relay.sql.sha256"
else
  printf '%s\n' 'Backup failed: do not use the partial SQL or continue the upgrade.' >&2
  exit 1
fi
```

只有 `mysqldump` 退出码为 0 且文件非空时才将 `.partial` 改为正式 SQL 并生成校验和。失败可能留下非空的半截 SQL，不能仅凭文件大小接受；停止本次升级，排除原因后重新备份。命令从容器私有环境读取密码，不将密码放到命令行参数。`--single-transaction` 适用于当前 InnoDB 表，备份期间避免同时执行升级或 DDL。

将目录加密复制到独立存储，并在测试环境导入验证。仓库忽略 `backups/`，但这不替代文件访问控制与离机备份。

::: details 什么时候需要停中继再备份
日常一致性数据备份可使用事务快照。升级回滚点建议在维护时段停止 `server` 后备份，减少备份之后新增账号、归属和审计与回滚点不一致：

```sh
docker compose stop server
```

确认数据库导出成功后再进行升级；如果放弃维护，执行 `docker compose start server` 恢复原实例。停止中继会结束设备连接，MySQL / Redis 仍保持运行。
:::

## MySQL 恢复：先恢复到新空库

优先把备份恢复到**新数据库**进行核对，再切换中继 DSN。不要一边让服务写入，一边把旧 SQL 覆盖到生产库；旧备份导入现有库也未必移除新版本额外创建的表。

### 1. 准备恢复目标

在维护时段停止中继，保留当前数据与配置副本：

```sh
docker compose stop server
docker compose exec mysql mysql -u root -p
```

第二条进入 MySQL 管理会话并提示输入当前 root 密码。在该会话创建全新恢复库；以下以专用 Compose 实例的 `embeddesk` 账号为例，确认数据库名尚未使用：

```sql
CREATE DATABASE embeddesk_restore CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
GRANT ALL PRIVILEGES ON embeddesk_restore.* TO 'embeddesk'@'%';
```

退出 MySQL。若使用外部数据库或自定义账号，改由现有数据库管理流程建立相应库与授权，不照搬账号 Host。

### 2. 导入已校验的备份

替换 `SNAPSHOT` 为实际备份目录：

```sh
sha256sum --check backups/SNAPSHOT/relay.sql.sha256
docker compose exec -T mysql sh -c 'MYSQL_PWD="$MYSQL_PASSWORD" exec mysql -u embeddesk embeddesk_restore' < backups/SNAPSHOT/relay.sql
```

导入必须成功退出。恢复期间不要将这个节点公开接流量，也不要让测试实例与生产共用活动设备身份和 Redis 会话。

### 3. 切换 DSN 与匹配版本

在私有 `.env` 中将 DSN 的数据库名改为 `embeddesk_restore`，其他认证信息与实际授权保持一致。配合**与备份匹配的程序版本**启动；若是版本回滚，先按下文选择旧镜像。

切库后还需同步两处，避免“服务使用新库，备份和健康检查仍访问旧库”：

1. 将后续 `mysqldump` 命令最后的数据库参数从 `embeddesk` 改为 `embeddesk_restore`；本页备份命令以首次部署的默认库为例，实际备份始终跟随当前中继 DSN。
2. 修改部署用 `compose.yml` 的 MySQL `healthcheck.test`，将 `-D embeddesk` 改为 `-D embeddesk_restore`；原文件中的 `-D` 与库名也可能连写，按实际内容替换。保留命令中的密码环境变量引用，不把真实密码写进检查命令。

健康检查定义修改后重建 MySQL 容器，命名卷保持不变。此操作会短暂重启 MySQL，应在中继已停止的维护时段进行：

```sh
docker compose config --quiet
docker compose up -d --no-build mysql
docker compose ps mysql
docker inspect --format '{{json .Config.Healthcheck.Test}}' "$(docker compose ps -q mysql)"
```

确认 MySQL 恢复为 `healthy`，最后一条仅查看实际容器的健康检查定义，库名应为 `embeddesk_restore`，不要改为输出完整容器环境。再核对新库可以查询：

```sh
docker compose exec -T mysql sh -c 'MYSQL_PWD="$MYSQL_PASSWORD" exec mysql -u embeddesk -D embeddesk_restore -Nse "SELECT DATABASE()"'
```

预期只返回 `embeddesk_restore`。核对生效的 `.env` DSN 与后续备份脚本也使用这个名称，再继续启动中继。原库保留期间，查旧库成功不能作为新库正常的依据。

Redis 可能仍保存恢复点之后的控制台会话。恢复演练建议使用隔离 Redis；实际恢复时可选择该专用 Redis 中一个确认未使用的逻辑库，并同步修改 URL（例如从 `/0` 到 `/1`），让用户重新登录。不要对共享 Redis 使用 `FLUSHALL`。

```sh
docker compose up -d --no-build --no-deps server
curl --fail --silent --show-error http://127.0.0.1:7080/ready
```

### 4. 恢复后核对

检查管理员可登录、用户 / 设备归属 / 节点地址正确，再验证双端授权和转发。旧数据库先保留为受保护的恢复材料，确认完整后按自己的保留策略清理。数据库恢复不会把活动串口、端口连接或设备授权带回来。

## SQLite 备份与恢复

当前 SQLite 启用 WAL。不能只复制正在写入的主 `.sqlite` 文件。最容易核实的流程是在维护时段停止服务，再保留主文件以及仍存在的 WAL / SHM 文件。

下面按 systemd 示例路径操作，先检查你自己的数据库路径与服务名：

```sh
sudo systemctl stop embeddesk-server
relay_sqlite_backup_dir="/var/backups/embeddesk/$(date -u +%Y%m%dT%H%M%SZ)"
sudo install -d -o root -g root -m 700 "$relay_sqlite_backup_dir"
sudo cp -a /etc/embeddesk/server.env /opt/embeddesk/embeddesk-server "$relay_sqlite_backup_dir/"
for relay_db_file in /var/lib/embeddesk/relay.sqlite /var/lib/embeddesk/relay.sqlite-wal /var/lib/embeddesk/relay.sqlite-shm; do
  if sudo test -f "$relay_db_file"; then
    sudo cp -a "$relay_db_file" "$relay_sqlite_backup_dir/"
  fi
done
sudo test -s "$relay_sqlite_backup_dir/relay.sqlite"
sudo systemctl start embeddesk-server
```

备份文件保留为同一组，主文件不能为空。在隔离实例中尝试打开并检查账号、设备和节点信息，才算完成恢复演练。也可使用 SQLite 官方在线备份机制，但不要用普通文件复制冒充在线一致性备份。

::: details 停机恢复到 systemd 示例目录
1. 停止服务，并对当前状态另做一次停机备份。
2. 将当前 `relay.sqlite`、`relay.sqlite-wal`、`relay.sqlite-shm` 一起移入单独的私有保留目录，避免新旧 WAL 混用。
3. 将所选快照的主文件及其同组 WAL / SHM 复制回 `/var/lib/embeddesk/`。
4. 对恢复的文件设置 `embeddesk:embeddesk`、600 权限，目录维持 700；不要把备份目录放进服务可公开访问的路径。
5. 用匹配版本二进制启动，检查 `/ready`、管理员登录、归属和实际转发。

SQLite 备份不会自动导入 MySQL。换数据库驱动属于数据迁移，需要单独验证字段、密码摘要、ID 和归属一致性。
:::

## Compose 升级与回滚

### 升级前

1. 安排维护，确认重启会结束授权和转发。
2. 记录 `git rev-parse HEAD`、当前镜像与部署配置。
3. 按上述流程保存一致性数据库备份，并保留上一版镜像。

默认镜像名的示例：

```sh
docker image tag embeddesk-relay:local embeddesk-relay:before-upgrade
```

使用自定义 `EMBEDDESK_IMAGE` 时，换成真实镜像名。不要执行镜像清理把这个回滚副本删除。

### 构建并切换

```sh
git pull --ff-only
docker compose build server
docker compose up -d --no-deps server
curl --fail --silent --show-error http://127.0.0.1:7080/ready
```

完成控制台、当前连接代码和双客户端转发检查。源码工作区有自己的修改时先审查，不用强制重置覆盖私有变更。仅部署中继升级时，不必顺带升级 MySQL / Redis 大版本。

### 需要回滚时

::: warning 旧程序不一定兼容迁移后的数据库
当前没有数据库降级命令。新程序启动时会执行 GORM 增量迁移；仅换回旧镜像不能保证恢复。如果表结构或数据含义变化，应恢复升级前备份到新空库，再使用匹配旧程序启动。回滚会舍弃恢复点之后的业务管理变更。
:::

确认数据库仍兼容，或已按恢复流程切换到备份库后，在 `.env` 设置 `EMBEDDESK_IMAGE=embeddesk-relay:before-upgrade`：

```sh
docker compose up -d --no-build --no-deps server
curl --fail --silent --show-error http://127.0.0.1:7080/ready
```

保留 `--no-build`，避免再次覆盖旧镜像。检查恢复内容，重新登录并验证转发后再对外宣布恢复。

## systemd 升级

维护前保留数据库和旧二进制。假设上传的新程序文件名为 `embeddesk-server.new`：

```sh
sudo systemctl stop embeddesk-server
sudo cp -a /opt/embeddesk/embeddesk-server /opt/embeddesk/embeddesk-server.previous
sudo install -o root -g root -m 755 embeddesk-server.new /opt/embeddesk/embeddesk-server
sudo systemctl start embeddesk-server
curl --fail --silent --show-error http://127.0.0.1:7080/ready
```

停止后再替换，避免覆盖正运行的文件。回滚同样先停服务，确认数据库兼容或恢复匹配备份，再安装 `.previous` 为正式程序并启动。

## 容量与扩容边界

当前上限包括 1024 个在线设备、4096 个设备会话、每设备 16 个会话，并为报文和队列设限。这些是保护上限，不是当前主机必然可以承载的性能承诺。

Redis 不提供跨实例路由，不能直接把多个进程放进随机负载均衡；普通反向代理也不会卸载 Go 进程的 WebSocket 转发负载。详见[设备协议边界](../DeviceConnections.md)。
