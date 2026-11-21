// 浏览器管理台的标志、版本号与导航对齐:UC-26 起只有浏览器一种承载,
// 不再有桌面标题栏那一套(mac 红绿灯 inset、tb-drag 拖拽行)。
import { test, expect } from '@playwright/test';
import { mkdirSync } from 'node:fs';
import { startEnv, login } from './env.mjs';
let env;
const VERSION = 'v0.9.17';
const DISPLAY_VERSION = 'v0.9.17';
test.use({ deviceScaleFactor: 2, viewport: { width: 1280, height: 820 } });
test.beforeAll(async () => {
  env = await startEnv();
  await env.seed('node', 'add', 'demo-node', '--address', '203.0.113.10', '--ssh-port', '1');
});
test.afterAll(async () => { await env?.stop(); });
async function setup(page, theme, version = VERSION) {
  await page.addInitScript(theme => {
    localStorage.setItem('mpx-theme', theme);
  }, theme);
  await page.route('**/api/admin/meta', async r => {
    const response = await r.fetch();
    await r.fulfill({ response, json: { ...(await response.json()), version } });
  });
  await login(page, env);
  await page.goto(env.origin + '/#nodes');
  await expect(page.locator('html[data-loading]')).toHaveCount(0);
  await page.evaluate(() => document.fonts.ready);
}
async function dimensions(page) {
  return page.evaluate(() => {
    const box = selector => { const r = document.querySelector(selector).getBoundingClientRect(); return { left: r.left, right: r.right, top: r.top, height: r.height }; };
    const brand = document.querySelector('.brand small');
    const probe = document.createElement('span');
    Object.assign(probe.style, { display: 'inline-block', width: '0', height: '0', verticalAlign: 'baseline' });
    brand.append(probe);
    const baseline = probe.getBoundingClientRect().bottom; probe.remove();
    return { logo: box('.brand img.lk' + (document.documentElement.dataset.theme === 'dark' ? '.lk-d' : '.lk-l')),
      icon: box('.nav.on svg'), pill: box('.cp-menu .profile'), top: box('.top'), nav: box('.nav.on'),
      version: box('.brand small'), count: box('.nav.on .ct'),
      padding: getComputedStyle(document.querySelector('.side')).paddingTop,
      baseline, versionFits: brand.scrollWidth <= brand.clientWidth, sideOverflow: document.querySelector('.side').scrollWidth > document.querySelector('.side').clientWidth };
  });
}
async function aligned(page, version = VERSION, display = DISPLAY_VERSION) {
  const el = page.locator('.brand small');
  await expect(el).toHaveText(display);
  await expect(el).toHaveCSS('font-size', '11.5px');
  await expect(el).toHaveCSS('margin-top', '6.5px');
  const style = await el.evaluate(e => {
    const s = getComputedStyle(e);
    // 直接解析 --faint 做基准:当前项计数继承品牌高亮色,拿页面元素会借错值。
    const probe = document.createElement('b');
    probe.style.color = 'var(--faint)';
    e.append(probe);
    const faint = getComputedStyle(probe).color;
    probe.remove();
    return { family: s.fontFamily, faint, overflow: s.textOverflow };
  });
  expect(style.family).toContain('Mona Sans Mono VF');
  expect(style.overflow).not.toBe('ellipsis');
  await expect(el).toHaveCSS('color', style.faint);
  await expect(page.locator('.brand small')).toHaveAttribute('title', version);
  const m = await dimensions(page);
  console.log('LOGO_ALIGN ' + JSON.stringify(m));
  expect(m.logo.left).toBe(22); expect(m.logo.left).toBe(m.icon.left);
  // 控制面胶囊搬到顶栏:竖向居中、不越过顶栏右缘,正常可点。
  expect(m.pill.top).toBeGreaterThanOrEqual(m.top.top);
  expect(m.pill.top + m.pill.height).toBeLessThanOrEqual(m.top.top + m.top.height);
  expect(m.pill.right).toBeLessThanOrEqual(m.top.right);
  expect(m.version.right).toBeCloseTo(m.count.right, 1);
  expect(m.versionFits).toBe(true); expect(m.sideOverflow).toBe(false);
  expect(Math.abs(m.baseline - (m.logo.top + m.logo.height * 18 / 24))).toBeLessThanOrEqual(0.5);
  expect(m.logo.top).toBe(20);
}
for (const theme of ['light', 'dark']) {
  test(`${theme} 标志、版本、卡片对齐`, async ({ page }) => {
    await setup(page, theme);
    await aligned(page);
    if (process.env.MPX_LOGO_ALIGN_SHOTS && theme === 'light') {
      const dir = process.env.MPX_LOGO_ALIGN_SHOTS; mkdirSync(dir, { recursive: true });
      await page.screenshot({ path: `${dir}/browser-${theme}-crop.png`, clip: { x: 0, y: 0, width: 240, height: 200 } });
    }
  });
}

for (const [version, display] of [
  ['v0.9.11', 'v0.9.11'],
  ['0.9.11', 'v0.9.11'],
  ['v0.9.11-beta.2', 'v0.9.11'],
]) test('版本短显示与完整提示 · ' + version, async ({ page }) => {
  await setup(page, 'light', version);
  await aligned(page, version, display);
});
