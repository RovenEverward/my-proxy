<script setup lang="ts">
// 桌面窗口截图：浅色、深色各一张，跟随站点主题切换。图在 docs/public/ui/<name>.png 与 <name>-dark.png。
// size="dialog"：图已裁到对话框本身，按图片自身比例显示，不撑满整栏。
// 每张图都能点开看原图（新标签页），手机上整窗截图缩得很小时用得上。
import { withBase } from 'vitepress'
defineProps<{ name: string; cap: string; size?: 'dialog' }>()
</script>

<template>
  <figure class="tile win" :class="{ dialog: size === 'dialog' }">
    <template v-if="size === 'dialog'">
      <a class="only-light" :href="withBase(`/ui/${name}.png`)" target="_blank" rel="noopener"><img :src="withBase(`/ui/${name}.png`)" :alt="cap" loading="lazy"></a>
      <a class="only-dark" :href="withBase(`/ui/${name}-dark.png`)" target="_blank" rel="noopener"><img :src="withBase(`/ui/${name}-dark.png`)" :alt="cap" loading="lazy"></a>
    </template>
    <template v-else>
      <a class="only-light" :href="withBase(`/ui/${name}.png`)" target="_blank" rel="noopener"><img :src="withBase(`/ui/${name}.png`)" width="1280" height="820" :alt="cap" loading="lazy"></a>
      <a class="only-dark" :href="withBase(`/ui/${name}-dark.png`)" target="_blank" rel="noopener"><img :src="withBase(`/ui/${name}-dark.png`)" width="1280" height="820" :alt="cap" loading="lazy"></a>
    </template>
    <figcaption>{{ cap }}<span>MPX 桌面窗口</span></figcaption>
  </figure>
</template>
