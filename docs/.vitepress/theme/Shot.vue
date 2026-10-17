<script setup lang="ts">
// 桌面窗口截图：浅色、深色各一张，跟随站点主题切换。图在 docs/public/ui/<name>.png 与 <name>-dark.png。
// size="dialog"：图已裁到对话框本身，按图片自身比例显示，不撑满整栏。
import { withBase } from 'vitepress'
defineProps<{ name: string; cap: string; size?: 'dialog' }>()
</script>

<template>
  <figure class="tile win" :class="{ dialog: size === 'dialog' }">
    <template v-if="size === 'dialog'">
      <img class="only-light" :src="withBase(`/ui/${name}.png`)" :alt="cap" loading="lazy">
      <img class="only-dark" :src="withBase(`/ui/${name}-dark.png`)" :alt="cap" loading="lazy">
    </template>
    <template v-else>
      <img class="only-light" :src="withBase(`/ui/${name}.png`)" width="1280" height="820" :alt="cap" loading="lazy">
      <img class="only-dark" :src="withBase(`/ui/${name}-dark.png`)" width="1280" height="820" :alt="cap" loading="lazy">
    </template>
    <figcaption>{{ cap }}<span>MPX 桌面窗口</span></figcaption>
  </figure>
</template>
