import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { defineConfig } from 'vitepress'

const repository = 'https://github.com/Daofengql/EmbedDesk.Relay'
const base = process.env.DOCS_BASE ?? '/EmbedDesk.Relay/'
const docsRoot = fileURLToPath(new URL('..', import.meta.url))

if (!base.startsWith('/') || !base.endsWith('/')) {
  throw new Error('DOCS_BASE must start and end with /')
}

export default defineConfig({
  lang: 'zh-CN',
  title: 'EmbedDesk.Relay',
  description: 'EmbedDesk 开源中继服务器部署指南：Docker Compose、Nginx HTTPS、Linux 单二进制、配置与运维。',
  base,
  appearance: 'dark',
  cleanUrls: true,
  lastUpdated: true,
  head: [['link', { rel: 'icon', type: 'image/svg+xml', href: `${base}relay.svg` }]],
  sitemap: { hostname: `https://daofengql.github.io${base}` },
  themeConfig: {
    logo: '/relay.svg',
    siteTitle: 'EmbedDesk.Relay 文档',
    nav: [
      { text: '部署指南', link: '/deployment/', activeMatch: '/deployment/' },
      { text: '配置与运维', link: '/reference/configuration', activeMatch: '/reference/' },
      { text: '开发', link: '/development/', activeMatch: '/development/' },
      { text: '客户端文档', link: 'https://daofengql.github.io/EmbedDesk/' }
    ],
    sidebar: [
      {
        text: '部署',
        items: [
          { text: '部署总览与快速开始', link: '/deployment/' },
          { text: 'Docker Compose', link: '/deployment/docker' },
          { text: 'Nginx / 宝塔与 HTTPS', link: '/deployment/nginx' },
          { text: 'Linux 二进制与 systemd', link: '/deployment/binary' }
        ]
      },
      {
        text: '配置与运维',
        items: [
          { text: '配置项与优先级', link: '/reference/configuration' },
          { text: '控制台、账号与带宽', link: '/reference/console' },
          { text: '健康检查、备份与升级', link: '/reference/operations' },
          { text: '常见问题', link: '/reference/faq' }
        ]
      },
      {
        text: '开发与协议',
        collapsed: true,
        items: [
          { text: '构建与验证', link: '/development/' },
          { text: '文档与 GitHub Pages', link: '/development/docs-site' },
          { text: '设备连接与协议边界', link: '/DeviceConnections' },
          { text: 'Cloudflare 边缘中继方案', link: '/CloudflareRelay' },
          { text: '许可证与第三方声明', link: '/reference/licenses' }
        ]
      }
    ],
    outline: { level: [2, 3], label: '本页内容' },
    socialLinks: [{ icon: 'github', link: repository }],
    editLink: { pattern: `${repository}/edit/main/docs/:path`, text: '在 GitHub 上编辑本页' },
    lastUpdated: { text: '最后更新', formatOptions: { dateStyle: 'medium' } },
    docFooter: { prev: '上一篇', next: '下一篇' },
    darkModeSwitchLabel: '切换主题',
    lightModeSwitchTitle: '切换到浅色主题',
    darkModeSwitchTitle: '切换到深色主题',
    sidebarMenuLabel: '文档目录',
    returnToTopLabel: '回到顶部',
    skipToContentLabel: '跳到正文',
    externalLinkIcon: true,
    notFound: { title: '页面未找到', quote: '可以从部署指南或搜索继续查找。', linkLabel: '返回文档首页', linkText: '返回文档首页' },
    search: {
      provider: 'local',
      options: {
        miniSearch: {
          options: {
            tokenize: (text) => Array.from(new Intl.Segmenter('zh-CN', { granularity: 'word' }).segment(text))
              .filter((part) => part.isWordLike)
              .map((part) => part.segment)
          },
          searchOptions: { prefix: true, fuzzy: 0.15 }
        },
        translations: {
          button: { buttonText: '搜索文档', buttonAriaLabel: '搜索文档' },
          modal: {
            displayDetails: '显示详细内容',
            resetButtonTitle: '清空搜索',
            backButtonTitle: '返回',
            noResultsText: '没有找到相关内容',
            footer: { selectText: '选择', navigateText: '切换', closeText: '关闭' }
          }
        }
      }
    },
    footer: { message: `原创内容采用 <a href="${repository}/blob/main/LICENSE">GPL-3.0-only</a>，第三方内容保留各自许可。` }
  },
  markdown: {
    config(md) {
      const renderImage = md.renderer.rules.image!
      md.renderer.rules.image = (tokens, index, options, env, self) => {
        const src = tokens[index].attrGet('src')
        const rendered = renderImage(tokens, index, options, env, self)
        if (!src?.startsWith('/images/')) return rendered
        const target = md.utils.escapeHtml(`${base.slice(0, -1)}${src}`)
        return `<a class="screenshot-link" href="${target}" target="_blank" rel="noopener" title="查看原图">${rendered}</a>`
      }
      const fence = md.renderer.rules.fence!
      md.renderer.rules.fence = (tokens, index, options, env, self) => {
        if (tokens[index].info.trim() === 'mermaid') {
          return `<MermaidDiagram :source="${md.utils.escapeHtml(JSON.stringify(tokens[index].content))}" />`
        }
        return fence(tokens, index, options, env, self)
      }

      // Keep repository-relative Markdown links useful on both GitHub and Pages.
      md.core.ruler.after('inline', 'repository-links', (state) => {
        const currentFile = state.env.path ?? state.env.filePath
        if (!currentFile) return
        const visit = (tokens) => {
          for (const token of tokens) {
            if (token.type === 'link_open') {
              const href = token.attrGet('href')
              if (href && !/^(?:[a-z][a-z\d+.-]*:|\/|#)/i.test(href)) {
                const [pathname, suffix = ''] = href.split(/(?=[?#])/s, 2)
                const resolved = path.resolve(path.dirname(currentFile), decodeURI(pathname))
                const relative = path.relative(docsRoot, resolved).replaceAll('\\', '/')
                if (relative.startsWith('../') || pathname.endsWith('.txt')) {
                  const repoPath = path.relative(path.dirname(docsRoot), resolved).replaceAll('\\', '/')
                  token.attrSet('href', `${repository}/blob/main/${repoPath}${suffix}`)
                }
              }
            }
            if (token.children) visit(token.children)
          }
        }
        visit(state.tokens)
      })
    }
  }
})
