# 文档与 GitHub Pages

文档站使用 VitePress，与客户端文档保持相同的中文导航、搜索、主题切换和编辑入口。根目录的 npm 项目只构建文档，`www/` 的 npm 项目构建中继控制台，两者各自保留锁文件。

## 本机预览

```sh
npm ci
npm run docs:dev
```

生产构建与预览：

```sh
npm run docs:build
npm run docs:preview
```

默认站点路径为 `/EmbedDesk.Relay/`，发布地址是：

<https://daofengql.github.io/EmbedDesk.Relay/>

如果部署到自定义域名根目录，构建时可设置 `DOCS_BASE=/`；它必须以 `/` 开头和结尾。不要因此修改服务端的 `/console/` 路径，文档站与中继运行路径无关。

## Pages 工作流

[.github/workflows/docs.yml](../../.github/workflows/docs.yml) 在 `main` 文档相关变更后构建并上传 Pages 产物；Pull Request 只做构建，不发布。也可从 GitHub Actions 手动触发。

仓库 Settings → Pages 的构建来源设为 **GitHub Actions**。工作流通过 Pages / OIDC 权限发布静态产物，不需要把 `dist` 提交到分支。文档生成输出为 `docs/.vitepress/dist`，已由 Git 忽略。

修改后运行 `npm run docs:build` 检查死链，再通过预览检查桌面和窄屏导航、搜索与代码块。部署完成后检查首页、部署页和搜索资源均使用正确的仓库子路径。
