import { defineConfig } from 'vitepress'

export default defineConfig({
  // 项目页站点的地址是 https://<用户名>.github.io/<仓库名>/,资源必须带这个前缀才
  // 找得到;本地 `npm run docs:dev` 同样落在 /my-proxy/ 下,和线上一致。
  base: '/my-proxy/',

  // design.md、docs/internal 与 docs/atlas 是给维护者看的工程文档,保留在仓库里但不进公开站点。
  srcExclude: ['design.md', 'internal/**', 'atlas/**'],

  lang: 'zh-CN',
  title: 'MPX',
  description: 'MPX：在自己电脑上管好几台代理 VPS。装机、加人、加服务器、升级都在桌面程序里点，不用登 VPS。',
  head: [
    ['link', { rel: 'icon', href: '/my-proxy/favicon.svg', type: 'image/svg+xml' }],
    ['link', { rel: 'icon', href: '/my-proxy/favicon.ico', sizes: 'any' }],
    ['link', { rel: 'apple-touch-icon', href: '/my-proxy/apple-touch-icon.png' }],
    ['meta', { name: 'color-scheme', content: 'light dark' }],
    ['script', {}, '(() => {\n  let mode = "system"; try { mode = localStorage.getItem("theme") || "system"; } catch {}\n  const q = matchMedia("(prefers-color-scheme: dark)");\n  const apply = () => { document.documentElement.dataset.theme = mode === "system" ? (q.matches ? "dark" : "light") : mode; };\n  q.addEventListener("change", () => mode === "system" && apply());\n  window.cycleTheme = () => { mode = { system: "light", light: "dark", dark: "system" }[mode];\n    try { mode === "system" ? localStorage.removeItem("theme") : localStorage.setItem("theme", mode); } catch {} apply(); };\n  apply();\n})();']
  ],
  appearance: false,

  themeConfig: {
    logo: { light: '/mark-light.png', dark: '/mark-light.png' },
    siteTitle: 'MPX',

    nav: [
      { text: '教程', link: '/tutorial/quickstart' },
      { text: '指南', link: '/guides/add-users' },
      { text: '参考', link: '/reference/interface' },
      { text: '原理', link: '/concepts/architecture' }
    ],

    socialLinks: [{ icon: 'github', link: 'https://github.com/RovenEverward/my-proxy' }],

    sidebar: [
      {
        text: '教程',
        items: [{ text: '快速开始', link: '/tutorial/quickstart' }]
      },
      {
        text: '常用',
        items: [
          { text: '添加用户并分发订阅', link: '/guides/add-users' },
          { text: '添加节点', link: '/guides/add-nodes' },
          { text: '一键更新', link: '/guides/update' },
          { text: '用体检排查', link: '/guides/troubleshoot' }
        ]
      },
      {
        text: '进阶',
        items: [
          { text: '用套餐分配节点', link: '/guides/plans' },
          { text: '设置分流规则', link: '/guides/routing' },
          { text: '让某个客户端拿到指定格式', link: '/guides/client-format' },
          { text: '在多台设备上管理', link: '/guides/devices' },
          { text: '备份控制面', link: '/guides/backup' }
        ]
      },
      {
        text: '参考',
        items: [
          { text: '界面', link: '/reference/interface' },
          { text: '订阅设置', link: '/reference/settings' },
          { text: '订阅接口', link: '/reference/subscription' },
          { text: '命令行', link: '/reference/cli' }
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
      message: 'MPX — 在自己电脑上管好你那几台 VPS',
      copyright: '文档与代码同仓库维护'
    }
  }
})
