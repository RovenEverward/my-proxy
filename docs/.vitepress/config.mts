import { defineConfig } from 'vitepress'

export default defineConfig({
  lang: 'zh-CN',
  title: 'mpx',
  description: '自建 xray 多节点代理的控制面：一台控制面管住所有节点，本地命令行是全部入口。',
  head: [['link', { rel: 'icon', href: '/logo.svg' }]],

  themeConfig: {
    logo: '/logo.svg',
    siteTitle: 'mpx',

    nav: [
      { text: '使用指南', link: '/user/' },
      {
        text: '参考手册',
        items: [
          { text: '全局配置字典', link: '/user/reference/settings' },
          { text: '协议与传输层支持矩阵', link: '/user/reference/protocols' },
          { text: '用户 CSV 导入导出规格', link: '/user/reference/csv-format' }
        ]
      },
      { text: '内部工程', link: '/internal/' }
    ],

    sidebar: {
      '/user/': [
        {
          text: '开始',
          items: [
            { text: '文档导航', link: '/user/' },
            { text: '从零搭建多节点代理管理服务', link: '/user/tutorial/getting-started' }
          ]
        },
        {
          text: '操作指南',
          items: [
            { text: '客户端订阅导入与使用', link: '/user/guides/client-setup' },
            { text: '节点运维与生命周期管理', link: '/user/guides/node-operations' },
            { text: '用户与配额管理', link: '/user/guides/user-management' },
            { text: '告警与到期通知配置', link: '/user/guides/notifications' },
            { text: '备份与灾备恢复实操', link: '/user/guides/backup-and-restore' },
            { text: '故障排查手册', link: '/user/guides/troubleshooting' }
          ]
        },
        {
          text: '参考速查',
          items: [
            { text: '全局配置字典', link: '/user/reference/settings' },
            { text: '协议与传输层支持矩阵', link: '/user/reference/protocols' },
            { text: '用户 CSV 导入导出规格', link: '/user/reference/csv-format' }
          ]
        }
      ],
      '/internal/': [
        {
          text: '概览',
          items: [{ text: 'mpx 是怎么搭起来的', link: '/internal/' }]
        },
        {
          text: '设计与契约',
          items: [
            { text: '对外契约', link: '/internal/contracts' },
            { text: '数据模型与语义', link: '/internal/model' }
          ]
        },
        {
          text: '决策与现状',
          items: [
            { text: '关键决策与理由', link: '/internal/decisions' },
            { text: '现状', link: '/internal/status' },
            { text: '路线图', link: '/internal/roadmap' },
            { text: '缺口与待定项', link: '/internal/open-questions' }
          ]
        }
      ]
    },

    outline: { label: '本页目录', level: [2, 3] },
    docFooter: { prev: '上一篇', next: '下一篇' },
    darkModeSwitchLabel: '外观',
    lightModeSwitchTitle: '切换到浅色',
    darkModeSwitchTitle: '切换到深色',
    sidebarMenuLabel: '目录',
    returnToTopLabel: '回到顶部',
    langMenuLabel: '切换语言',

    search: {
      provider: 'local',
      options: {
        translations: {
          button: { buttonText: '搜索文档', buttonAriaLabel: '搜索文档' },
          modal: {
            displayDetails: '显示详情',
            resetButtonTitle: '清除查询',
            backButtonTitle: '返回',
            noResultsText: '没有找到相关内容',
            footer: {
              selectText: '选择',
              selectKeyAriaLabel: '回车',
              navigateText: '切换',
              navigateUpKeyAriaLabel: '上箭头',
              navigateDownKeyAriaLabel: '下箭头',
              closeText: '关闭',
              closeKeyAriaLabel: 'Esc'
            }
          }
        }
      }
    },

    footer: {
      message: 'mpx — 自建 xray 多节点代理的控制面',
      copyright: '文档与代码同仓库维护'
    }
  }
})
