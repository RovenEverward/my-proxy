<script setup lang="ts">
import { computed, onMounted, watch, nextTick } from 'vue'
import { useData, useRoute, withBase } from 'vitepress'

const { theme, frontmatter, page } = useData()
const route = useRoute()
const isHome = computed(() => frontmatter.value.layout === 'home')
const norm = (p: string) => p.replace(/\.html$/, '').replace(/\/$/, '')
const cur = computed(() => norm('/' + page.value.relativePath.replace(/\.md$/, '')))
const groups = computed(() => theme.value.sidebar as any[])
const inGroup = (g: any) => g.items.some((i: any) => norm(i.link) === cur.value)

const ICON = '<svg class="i-copy" viewBox="0 0 16 16" aria-hidden="true"><rect x="5.5" y="5.5" width="8" height="8" rx="2"/><path d="M10.5 3.5v-.5a1.5 1.5 0 0 0-1.5-1.5H4A1.5 1.5 0 0 0 2.5 3v5A1.5 1.5 0 0 0 4 9.5h.5"/></svg><svg class="i-ok" viewBox="0 0 16 16" aria-hidden="true"><path d="M3.5 8.5l3 3 6-7"/></svg>'
const LANG: Record<string, string> = { bash: 'Shell', console: 'Shell', json: 'JSON', http: 'HTTP', text: 'Text' }

function enhance() {
  document.querySelectorAll<HTMLElement>('.copy[data-copy]:not([data-bound])').forEach((b) => {
    b.dataset.bound = ''
    b.addEventListener('click', async () => {
      try {
        await navigator.clipboard.writeText(document.getElementById(b.dataset.copy!)!.textContent || '')
        b.dataset.done = ''; b.setAttribute('aria-label', '已复制')
        setTimeout(() => { delete b.dataset.done; b.setAttribute('aria-label', '复制代码') }, 1400)
      } catch {}
    })
  })
  document.querySelectorAll<HTMLElement>('main div[class*="language-"]:not([data-done])').forEach((el) => {
    el.dataset.done = ''
    const lang = (el.className.match(/language-(\w+)/) || [])[1] || ''
    const bar = document.createElement('div')
    bar.className = 'code-bar'
    bar.innerHTML = `${LANG[lang] || lang}<button class="copy" type="button" aria-label="复制代码" title="复制">${ICON}</button>`
    el.prepend(bar)
    const b = bar.querySelector('button')!
    b.addEventListener('click', async () => {
      try {
        let t = el.querySelector('pre')!.textContent || ''
        if (lang === 'console') t = t.split('\n').filter((l) => l.startsWith('$ ')).map((l) => l.slice(2)).join('\n')
        await navigator.clipboard.writeText(t)
        b.dataset.done = ''; b.setAttribute('aria-label', '已复制')
        setTimeout(() => { delete b.dataset.done; b.setAttribute('aria-label', '复制代码') }, 1400)
      } catch {}
    })
  })
}
onMounted(enhance)
watch(() => route.path, () => nextTick(enhance))
const cycle = () => (window as any).cycleTheme?.()
</script>

<template>
  <nav><div class="wrap">
    <a class="brand" :href="withBase('/')">MPX<small>文档</small></a>
    <a class="l hide-s" :href="withBase('/tutorial/quickstart')">快速开始</a>
    <a class="l hide-s" :href="withBase('/guides/add-users')">指南</a>
    <a class="l hide-s" :href="withBase('/reference/cli')">参考</a>
    <a class="l hide-s" :href="withBase('/concepts/architecture')">原理</a>
    <a class="l" href="https://github.com/RovenEverward/my-proxy" aria-label="GitHub">GitHub</a>
    <button class="l" type="button" @click="cycle" aria-label="切换主题">◐</button>
    <a class="btn" :href="withBase('/tutorial/quickstart')">开始使用</a>
  </div></nav>

  <main v-if="page.isNotFound" class="wrap doc"><div></div><article><p class="eyebrow">404</p><h1>没有这一页</h1><p class="lede">链接可能已经变了。<a :href="withBase('/')">回到首页</a></p></article></main>

  <main v-else-if="isHome" class="home"><Content /></main>

  <main v-else class="wrap doc">
    <aside class="toc" aria-label="文档分类">
      <template v-for="g in groups" :key="g.text">
        <a :href="withBase(g.items[0].link)" class="cat" :class="{ on: inGroup(g) }" :aria-current="inGroup(g) ? 'true' : undefined">{{ g.text }}<span class="n">{{ g.items.length }}</span></a>
        <div v-if="inGroup(g)" class="pages">
          <a v-for="i in g.items" :key="i.link" :href="withBase(i.link)" :class="{ cur: norm(i.link) === cur }">{{ i.text }}</a>
        </div>
      </template>
    </aside>
    <article>
      <p class="eyebrow">文档 · {{ groups.find(inGroup)?.text }}</p>
      <Content class="md" />
    </article>
  </main>

  <footer><div class="wrap"><span>MPX · 文档与代码同仓库维护</span><div class="r">
    <a :href="withBase('/tutorial/quickstart')">快速开始</a>
    <a :href="withBase('/guides/add-users')">指南</a>
    <a :href="withBase('/reference/cli')">参考</a>
    <a :href="withBase('/concepts/architecture')">原理</a>
    <a href="https://github.com/RovenEverward/my-proxy">GitHub</a>
  </div></div></footer>
</template>
