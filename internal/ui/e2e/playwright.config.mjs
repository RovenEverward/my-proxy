// mpx ui 的浏览器测试:真起一个 mpx serve、一个 mpx ui,在 Chromium 里点。
// 跑法:npm run test:ui(第一次先 npx playwright install chromium)。
// globalSetup 只编一次 mpx,各 spec 直接用它(见 global-setup.mjs)。
import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: ".",
  testMatch: /.*\.spec\.mjs$/,
  globalSetup: "./global-setup.mjs",
  // 每个 spec 文件自己起一套控制面和 mpx ui,所以文件之间可以并行;文件内的用例串行。
  // 控制面的 /api 按 IP 限流,同一个文件里的请求才共用一个桶(测试进程把它放宽了)。
  workers: process.env.CI ? 2 : 4,
  fullyParallel: false,
  timeout: 30_000,
  expect: { timeout: 15_000 },
  reporter: [["list"]],
  use: {
    timezoneId: process.env.TZ || "Asia/Shanghai",
    locale: "zh-CN",
    viewport: { width: 1440, height: 900 },
    launchOptions: process.env.MPX_E2E_CHROMIUM ? { executablePath: process.env.MPX_E2E_CHROMIUM } : {},
  },
});
