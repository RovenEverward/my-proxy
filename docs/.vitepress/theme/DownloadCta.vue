<script setup lang="ts">
import { onMounted, ref } from 'vue'

const releases = 'https://github.com/RovenEverward/my-proxy/releases/latest'
// UC-26 起没有桌面安装包,只有 mpx 命令行:按访问者平台挑一行安装命令。
const SH = 'curl -fsSL https://raw.githubusercontent.com/RovenEverward/my-proxy/main/deploy/install-cli.sh | bash'
const PS = 'irm https://raw.githubusercontent.com/RovenEverward/my-proxy/main/deploy/install-cli.ps1 | iex'
const cmd = ref(SH)

type UserAgentData = {
  platform?: string
  mobile?: boolean
}

onMounted(() => {
  const nav = navigator as Navigator & { userAgentData?: UserAgentData }
  const data = nav.userAgentData
  if (data?.mobile || /android|iphone|ipad|ipod/i.test(nav.userAgent)) return
  const platform = data?.platform || nav.platform || nav.userAgent
  if (/windows|^win/i.test(platform)) cmd.value = PS
})

const copied = ref(false)
const copy = async () => {
  try { await navigator.clipboard.writeText(cmd.value); copied.value = true; setTimeout(() => (copied.value = false), 1500) } catch { /* 剪贴板不可用时保持原样 */ }
}
</script>

<template>
  <div class="cta">
    <code class="cta-cmd">{{ cmd }}</code>
    <button class="btn btn-lg" type="button" @click="copy">{{ copied ? '已复制' : '复制' }}</button>
    <a class="btn-ghost" :href="releases">各平台安装包</a>
  </div>
</template>

<style scoped>
.cta-cmd {
  display: inline-block;
  max-width: 100%;
  overflow-x: auto;
  white-space: nowrap;
  padding: 10px 14px;
  border-radius: 8px;
  font-size: 13px;
}
</style>
