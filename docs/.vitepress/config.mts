import { defineConfig } from 'vitepress'

export default defineConfig({
  // 项目页站点的地址是 https://<用户名>.github.io/<仓库名>/,资源必须带这个前缀才
  // 找得到;本地 `npm run docs:dev` 同样落在 /my-proxy/ 下,和线上一致。
  base: '/my-proxy/',

  // design.md 与 docs/internal 是给维护者看的工程文档,保留在仓库里但不进公开站点。
  srcExclude: ['design.md', 'internal/**'],

  lang: 'zh-CN',
  title: 'MPX',
  description: '自建 xray 多节点代理的控制面：一台控制面管住所有节点，本地命令行是全部入口。',
  head: [
    ['link', { rel: 'icon', href: '/my-proxy/logo.svg' }],
    ['meta', { name: 'color-scheme', content: 'light dark' }],
    ['script', {}, '(() => {\n  let mode = "system"; try { mode = localStorage.getItem("theme") || "system"; } catch {}\n  const q = matchMedia("(prefers-color-scheme: dark)");\n  const apply = () => { document.documentElement.dataset.theme = mode === "system" ? (q.matches ? "dark" : "light") : mode; };\n  q.addEventListener("change", () => mode === "system" && apply());\n  window.cycleTheme = () => { mode = { system: "light", light: "dark", dark: "system" }[mode];\n    try { mode === "system" ? localStorage.removeItem("theme") : localStorage.setItem("theme", mode); } catch {} apply(); };\n  apply();\n})();']
  ],
  appearance: false,

  themeConfig: {
    logo: '/logo.svg',
    siteTitle: 'MPX',

    nav: [
      { text: '教程', link: '/tutorial/quickstart' },
      { text: '指南', link: '/guides/add-users' },
      { text: '参考', link: '/reference/cli' },
      { text: '原理', link: '/concepts/architecture' }
    ],

    socialLinks: [{ icon: 'github', link: 'https://github.com/RovenEverward/my-proxy' }],

    sidebar: [
      {
        text: '教程',
        items: [
          { text: '快速开始', link: '/tutorial/quickstart' },
          { text: '在本机试用', link: '/tutorial/local-trial' }
        ]
      },
      {
        text: '指南',
        items: [
          { text: '添加用户并分发订阅', link: '/guides/add-users' },
          { text: '让某个客户端拿到指定格式', link: '/guides/client-format' },
          { text: '用 mpx doctor 排查', link: '/guides/troubleshoot' },
          { text: '用 mpx ui 在浏览器里管理', link: '/guides/web-ui' }
        ]
      },
      {
        text: '参考',
        items: [
          { text: '命令行', link: '/reference/cli' },
          { text: '订阅设置', link: '/reference/settings' },
          { text: '订阅接口', link: '/reference/subscription' }
        ]
      },
      {
        text: '原理',
        items: [{ text: '控制面与节点', link: '/concepts/architecture' }]
      }
    ],

    outline: { label: '本页目录', level: [2, 3] },
    docFooter: { prev: false, next: false },
    darkModeSwitchLabel: '外观',
    lightModeSwitchTitle: '切换到浅色',
    darkModeSwitchTitle: '切换到深色',
    sidebarMenuLabel: '目录',
    returnToTopLabel: '回到顶部',
    langMenuLabel: '切换语言',

    footer: {
      message: 'MPX — 自建多节点代理的控制面',
      copyright: '文档与代码同仓库维护'
    }
  }
})
