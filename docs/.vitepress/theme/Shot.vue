<script setup lang="ts">
// 桌面窗口截图：浅色、深色各一张，跟随站点主题切换。图在 docs/public/ui/<name>.png 与 <name>-dark.png。
import { withBase } from 'vitepress'
defineProps<{ name: string; cap: string }>()
</script>

<template>
  <figure class="tile win">
    <img class="only-light" :src="withBase(`/ui/${name}.png`)" width="1280" height="820" :alt="cap" loading="lazy">
    <img class="only-dark" :src="withBase(`/ui/${name}-dark.png`)" width="1280" height="820" :alt="cap" loading="lazy">
    <figcaption>{{ cap }}<span>MPX 桌面窗口</span></figcaption>
  </figure>
</template>
