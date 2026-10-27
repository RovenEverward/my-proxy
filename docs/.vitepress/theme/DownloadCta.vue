<script setup lang="ts">
import { onMounted, ref } from 'vue'

const releases = 'https://github.com/RovenEverward/my-proxy/releases/latest'
const label = ref('下载')
const href = ref(releases)

type UserAgentData = {
  platform?: string
  mobile?: boolean
  getHighEntropyValues?: (hints: string[]) => Promise<{ architecture?: string }>
}

onMounted(async () => {
  const nav = navigator as Navigator & { userAgentData?: UserAgentData }
  const data = nav.userAgentData
  const ua = nav.userAgent
  // 手机和平板也能访问文档，但没有对应的桌面安装包。
  if (data?.mobile || /android|iphone|ipad|ipod/i.test(ua) ||
      (nav.platform === 'MacIntel' && nav.maxTouchPoints > 1)) return

  const platform = data?.platform || nav.platform || ua
  const download = (file: string, text: string) => {
    href.value = `${releases}/download/${file}`
    label.value = text
  }

  if (/windows|^win/i.test(platform)) {
    download('MPX-Windows-x64-setup.exe', '下载 Windows 版')
  } else if (/mac|darwin/i.test(platform)) {
    download('MPX-macOS.dmg', '下载 macOS 版')
  } else if (/linux/i.test(platform)) {
    const linux = (arm: boolean) => download(
      `MPX-Linux-${arm ? 'arm64' : 'amd64'}.deb`, '下载 Linux 版（.deb）'
    )
    linux(/arm|aarch64/i.test(`${nav.platform} ${ua}`))
    // Chromium 的 UA 可能隐藏架构；支持时再读取 Client Hints。
    if (data?.getHighEntropyValues) {
      try {
        const { architecture } = await data.getHighEntropyValues(['architecture'])
        if (architecture) linux(/arm|aarch64/i.test(architecture))
      } catch {
        // 拒绝提供 Client Hints 时，保留 UA / platform 的判断。
      }
    }
  }
})
</script>

<template>
  <div class="cta">
    <a class="btn btn-lg download-primary" :href="href">{{ label }}</a>
    <a class="btn-ghost" :href="releases">其他平台</a>
  </div>
</template>

<style scoped>
.download-primary {
  min-width: 220px;
  justify-content: center;
}
</style>
