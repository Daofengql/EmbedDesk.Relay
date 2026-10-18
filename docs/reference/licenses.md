# 许可证与第三方声明

原创代码与文档采用 [GPL-3.0-only](../../LICENSE)，允许商业使用及收费分发；分发时仍需履行 GPL 对应义务。第三方依赖保留各自许可证、版权和来源说明。

| 内容 | 声明入口 |
| --- | --- |
| Go 与 Go 依赖原始许可 | [licenses/NOTICE.md](../../licenses/NOTICE.md) |
| React 控制台生产依赖 | [www/licenses/DEPENDENCIES.md](../../www/licenses/DEPENDENCIES.md) |
| 前端完整第三方许可 | [www/licenses/THIRD_PARTY_LICENSES.txt](../../www/licenses/THIRD_PARTY_LICENSES.txt) |

发布构建把原始许可证与控制台依赖许可嵌入单二进制，可执行 `embeddesk-server --licenses` 查看。控制台也提供 `/console/licenses/THIRD_PARTY_LICENSES.txt`。

MySQL、Redis、Nginx 与 Docker 是独立软件，不包含在中继二进制中，分别遵循自身许可。

## 文档站依赖

文档站与中继控制台分别构建。以下依赖仅用于 VitePress 静态文档及架构图，不属于中继运行依赖，也不嵌入 Go 中继二进制：

| 依赖 | 版本 | 许可与来源 |
| --- | --- | --- |
| VitePress | 1.6.4 | MIT；[npm](https://www.npmjs.com/package/vitepress/v/1.6.4)、[原始 LICENSE](https://github.com/vuejs/vitepress/blob/v1.6.4/LICENSE) |
| Mermaid | 11.17.2 | MIT；[npm](https://www.npmjs.com/package/mermaid/v/11.17.2)、[原始 LICENSE](https://github.com/mermaid-js/mermaid/blob/mermaid%4011.17.2/LICENSE) |

确切安装版本由根目录 `package-lock.json` 锁定，分发文档站时保留各依赖及其传递依赖的版权与许可。
