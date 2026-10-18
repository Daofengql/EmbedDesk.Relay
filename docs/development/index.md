# 构建与验证

开发需要 Go 1.26.9+、Node.js 22.12+（建议 24）和 npm。中继代码与控制台独立于 Windows 桌面项目，运行不依赖 .NET。

## 完整发布构建

仓库根目录执行：

```powershell
./build.ps1
./build.ps1 -TargetOS windows
```

默认生成 `artifacts/linux-amd64/embeddesk-server`；显式指定 Windows 时生成 `artifacts/windows-amd64/embeddesk-server.exe`。Linux / macOS 使用 `sh build.sh`，可通过 `GOOS` 和 `GOARCH` 指定目标。

构建脚本完成 npm 安装、TypeScript / Vite 构建、前端许可收集以及 Go 单二进制发布。生成目录不提交 Git。

## 代码目录

| 目录 | 用途 |
| --- | --- |
| `cmd/relay/` | 命令行入口 |
| `internal/relay/` | 配置、设备路由、控制台、存储和服务端测试 |
| `www/` | React / Ant Design 控制台与 Go 嵌入资源包 |
| `licenses/` | Go 依赖原始许可与 Go 嵌入资源包 |
| `deploy/` | Nginx、systemd 和二进制镜像示例 |
| `docs/` | VitePress 部署文档站 |

控制台构建完成后，可直接构建或运行命令入口：

```sh
mkdir -p artifacts
go build -o artifacts/embeddesk-server ./cmd/relay
go run ./cmd/relay
```

`go run` 同样读取启动环境，需要先配置管理员初始化和控制台开关；完整发布仍优先使用构建脚本。

## 前端开发

前后端分开运行时，Vite 页面和 API 必须使用同一个浏览器 Origin。下面固定前端为 `http://127.0.0.1:5173/console/`，Vite 将 `/api` 代理到后端 `127.0.0.1:7080`。

### 1. 启动隔离的开发后端

在仓库根目录的新开发终端执行，使用**尚未初始化的独立** `data/vite-dev.sqlite`，不要指向正式数据库。启动示例显式清空 JSON 配置入口与 Redis / MySQL 连接，避免沿用其他部署环境。首次密码通过隐藏输入提供，要求 12–72 个 UTF-8 字节。

::: code-group

```powershell [PowerShell]
$env:EMBEDDESK_CONFIG = ''
$env:EMBEDDESK_LISTEN = '127.0.0.1:7080'
$env:EMBEDDESK_DATABASE_DRIVER = 'sqlite'
$env:EMBEDDESK_DATABASE = 'data/vite-dev.sqlite'
$env:EMBEDDESK_MYSQL_DSN = ''
$env:EMBEDDESK_REDIS_URL = ''
$env:EMBEDDESK_CONSOLE_ENABLED = 'true'
$env:EMBEDDESK_REGISTRATION_ENABLED = 'false'
$env:EMBEDDESK_COOKIE_SECURE = 'false'
$env:EMBEDDESK_PUBLIC_URL = 'http://127.0.0.1:5173'
$env:EMBEDDESK_BOOTSTRAP_USERNAME = 'devadmin'
$relayDevPassword = Read-Host 'Development administrator password' -AsSecureString
$env:EMBEDDESK_BOOTSTRAP_PASSWORD = [Net.NetworkCredential]::new('', $relayDevPassword).Password
go run ./cmd/relay
Remove-Item Env:EMBEDDESK_BOOTSTRAP_PASSWORD
```

```bash [Bash]
read -r -s -p 'Development administrator password: ' relay_dev_password
printf '\n'
EMBEDDESK_CONFIG= \
EMBEDDESK_LISTEN=127.0.0.1:7080 \
EMBEDDESK_DATABASE_DRIVER=sqlite \
EMBEDDESK_DATABASE=data/vite-dev.sqlite \
EMBEDDESK_MYSQL_DSN= \
EMBEDDESK_REDIS_URL= \
EMBEDDESK_CONSOLE_ENABLED=true \
EMBEDDESK_REGISTRATION_ENABLED=false \
EMBEDDESK_COOKIE_SECURE=false \
EMBEDDESK_PUBLIC_URL=http://127.0.0.1:5173 \
EMBEDDESK_BOOTSTRAP_USERNAME=devadmin \
EMBEDDESK_BOOTSTRAP_PASSWORD="$relay_dev_password" \
go run ./cmd/relay
unset relay_dev_password
```

:::

保持后端终端运行，另开终端继续。开发完成后停止这个实例并关闭专用终端；不要在保留这些开发环境变量的终端里启动正式服务。如果 7080 已由正在使用的服务占用，选择其他空闲开发端口，并同步 `www/vite.config.ts` 的代理目标，不要停止现有连接。

::: warning 已初始化的节点不会被环境覆盖
`publicUrl` 首次写入 NodeSettings 后，环境变量不再覆盖数据库。若这个开发文件之前已经使用其他地址初始化，请选择另一个全新的开发数据库文件；不要删除或改写正式数据库来让示例生效。原示例的 `http://127.0.0.1:7080` 与 Vite 的 5173 Origin 不同，会导致登录或写请求被拒绝。
:::

### 2. 启动 Vite 并登录

```sh
cd www
npm ci
npm run dev -- --port 5173 --strictPort
```

打开 `http://127.0.0.1:5173/console/`，使用 `devadmin` 和刚输入的开发密码登录。`--strictPort` 防止端口被占用时 Vite 自动换端口，导致 Origin 再次不匹配。不要混用 `localhost` 与 `127.0.0.1`；如需变更前端地址，应使用相应独立开发节点的公开地址设置。

此配置中的 `cookieSecure=false` 仅用于回环 HTTP 开发。生产仍使用 HTTPS 与 Secure Cookie，控制台由 Go 二进制嵌入提供。React 界面使用 Ant Design，新增交互应沿用成熟组件并验证菜单、弹层与短窗口滚动。

## 自动验证

```sh
go test ./...
go vet ./...
```

前端验证：

```sh
npm --prefix www ci
npm --prefix www run build
```

外部 MySQL / Redis 测试仅在提供 `EMBEDDESK_TEST_MYSQL_DSN` 和 `EMBEDDESK_TEST_REDIS_URL` 时运行。目标必须为本次专用的空数据库，不使用生产库；Redis 使用随机前缀，不清理其他部署。

编译和自动测试不替代浏览器实际操作、Nginx 代理互通或硬件验收。桌面兼容验证在 [EmbedDesk 客户端仓库](https://github.com/Daofengql/EmbedDesk) 使用设备连接、TLS、流控和映射 Smoke 入口，需先构建本次中继和 Smoke 程序。

## 文档截图与示例数据

控制台截图放在 `docs/public/images/`，文章使用 `/images/文件名.png` 引用。当前概览、设备与批量操作截图来自 Linux 虚拟机中的隔离演示实例：独立端口、临时 SQLite 数据库，账号和设备均为专门生成的演示数据。

这些截图说明界面与操作位置，不代表生产配置或性能测试。概览中的空闲带宽为 0，SQLite 与未启用 Redis 的状态是演示环境的实际状态；生产推荐仍为 MySQL / Redis。设备标识来自临时随机凭据，不对应真实硬件或个人设备。

更新截图时先建立独立演示实例，不使用生产数据库、现有用户或真实会话。逐张确认没有密码、公共令牌、Cookie、私有地址及个人路径，再加入文档；截图完成后结束演示进程并撤销演示会话。

## 贡献约定

修改行为时同步相应文档；示例使用占位域名和回环地址，不记录真实账号、凭据或个人设备信息。协议版本保持明确，角色互斥、心跳、限速和有界队列不能因重构而退化。Worker 设计与 Go 实现状态见 [Cloudflare 方案](../CloudflareRelay.md)。
