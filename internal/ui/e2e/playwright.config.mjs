// mpx ui 的浏览器测试:真起一个 mpx serve、一个 mpx ui,在 Chromium 里点。
// 跑法:npm run test:ui(第一次先 npx playwright install chromium)。
import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: ".",
  testMatch: /.*\.spec\.mjs$/,
  // 控制面的 /api 按 IP 限流,所有请求都来自 127.0.0.1:串行跑,别让测试互相抢桶。
  workers: 1,
  fullyParallel: false,
  timeout: 90_000,
  expect: { timeout: 15_000 },
  reporter: [["list"]],
  use: {
    timezoneId: process.env.TZ || "Asia/Shanghai",
    locale: "zh-CN",
    viewport: { width: 1440, height: 900 },
    launchOptions: process.env.MPX_E2E_CHROMIUM ? { executablePath: process.env.MPX_E2E_CHROMIUM } : {},
  },
});
