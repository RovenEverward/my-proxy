import { test, expect } from '@playwright/test';
import { mkdirSync } from 'node:fs';
import { startEnv, login } from './env.mjs';
let env;
const VERSION = 'dev-aaaaaaaaaaaa-clean';
const DISPLAY_VERSION = 'dev-aaaaaaa';
test.use({ deviceScaleFactor: 2, viewport: { width: 1280, height: 820 } });
test.beforeAll(async () => {
  env = await startEnv();
  await env.seed('node', 'add', 'demo-node', '--address', '203.0.113.10', '--ssh-port', '1');
});
test.afterAll(async () => { await env?.stop(); });
async function setup(page, mode, theme, version = VERSION) {
  await page.addInitScript(({ mode, theme }) => {
    Object.defineProperty(navigator, 'platform', { value: mode === 'windows' ? 'Win32' : 'MacIntel' });
    Object.defineProperty(navigator, 'userAgentData', { value: { platform: mode === 'windows' ? 'Windows' : 'macOS' } });
    localStorage.setItem('mpx-theme', theme);
    document.addEventListener('DOMContentLoaded', () => {
      const s = document.documentElement.style;
      if (mode !== 'browser') s.setProperty('--mygo-titlebar-height', '52px');
      s.setProperty('--mygo-titlebar-inset-left', mode === 'mac' ? '70px' : '0px');
      s.setProperty('--mygo-titlebar-inset-right', mode === 'windows' ? '138px' : '0px');
    });
  }, { mode, theme });
  await page.route('**/ui/meta', async r => {
    const response = await r.fetch();
    await r.fulfill({ response, json: { ...(await response.json()), cli_version: version, server: 'https://203.0.113.10:8080' } });
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
      icon: box('.nav.on svg'), dot: box('.cpw .dot'), card: box('.cpw .profile'), nav: box('.nav.on'),
      version: box('.brand small'), count: box('.nav.on .ct'), drag: box('.side>.tb-drag'),
      padding: getComputedStyle(document.querySelector('.side')).paddingTop,
      dragRegion: getComputedStyle(document.querySelector('.side>.tb-drag')).getPropertyValue('--app-region').trim(),
      baseline, versionFits: brand.scrollWidth <= brand.clientWidth, sideOverflow: document.querySelector('.side').scrollWidth > document.querySelector('.side').clientWidth };
  });
}
async function aligned(page, mode, version = VERSION, display = DISPLAY_VERSION) {
  const el = page.locator('.brand small');
  await expect(el).toHaveText(display);
  await expect(el).toHaveCSS('font-size', '11px');
  await expect(el).toHaveCSS('margin-top', '7.5px');
  const style = await el.evaluate(e => {
    const s = getComputedStyle(e);
    return { family: s.fontFamily, faint: getComputedStyle(document.querySelector('.nav.on .ct')).color, overflow: s.textOverflow };
  });
  expect(style.family).toContain('Mona Sans Mono VF');
  expect(style.overflow).not.toBe('ellipsis');
  await expect(el).toHaveCSS('color', style.faint);
  await expect(page.locator('.brand small')).toHaveAttribute('title', version);
  const m = await dimensions(page);
  console.log('LOGO_ALIGN ' + JSON.stringify({ mode, ...m }));
  expect(m.logo.left).toBe(20); expect(m.logo.left).toBe(m.icon.left);
  expect(m.dot.left).toBe(21);
  expect(m.card.left).toBe(m.nav.left); expect(m.card.right).toBe(m.nav.right);
  expect(m.version.right).toBeCloseTo(m.count.right, 1);
  expect(m.versionFits).toBe(true); expect(m.sideOverflow).toBe(false);
  expect(Math.abs(m.baseline - (m.logo.top + m.logo.height * 18 / 24))).toBeLessThanOrEqual(0.5);
  expect(m.drag.height).toBe(mode === 'mac' ? 36 : 0);
  expect(m.padding).toBe(mode === 'mac' ? '0px' : '14px');
  expect(m.logo.top).toBe(mode === 'mac' ? 38 : 16);
  expect(m.dragRegion).toBe('drag');
}
for (const mode of ['mac', 'mac-fullscreen', 'browser', 'windows']) for (const theme of ['light', 'dark']) {
  test(`${mode} ${theme} 标志、版本、卡片对齐`, async ({ page }) => {
    await setup(page, mode, theme);
    await aligned(page, mode);
    if (process.env.MPX_LOGO_ALIGN_SHOTS && mode === 'mac' && theme === 'light') {
      const dir = process.env.MPX_LOGO_ALIGN_SHOTS; mkdirSync(dir, { recursive: true });
      await page.screenshot({ path: `${dir}/${mode}-${theme}-crop.png`, clip: { x: 0, y: 0, width: 240, height: 200 } });
    }
  });
}
test('Mac 进出全屏：inset 归零时收起拖拽行，恢复时重新占36px', async ({ page }) => {
  await setup(page, 'mac', 'light');
  await page.evaluate(() => { window.brandBefore = document.querySelector('.brand'); });
  for (const left of ['0px', '70px', '0px', '70px']) {
    await page.evaluate(left => {
      document.documentElement.style.setProperty('--mygo-titlebar-inset-left', left);
      window.dispatchEvent(new Event('resize'));
    }, left);
    await aligned(page, left === '0px' ? 'mac-fullscreen' : 'mac');
    expect(await page.evaluate(() => window.brandBefore === document.querySelector('.brand'))).toBe(true);
  }
});

for (const [version, display] of [
  ['v0.9.11', 'v0.9.11'],
  ['dev-aaaaaaaaaaaa-clean', 'dev-aaaaaaa'],
  ['dev-aaaaaaaaaaaa-dirty', 'dev-aaaaaaa*'],
  ['dev-aaaaaaaaaaaa', 'dev-aaaaaaaaaaaa'],
  ['dev-aaaaaaaaaaaaa', 'dev-aaaaaaa'],
]) test('版本短显示与完整提示 · ' + version, async ({ page }) => {
  await setup(page, 'browser', 'light', version);
  await aligned(page, 'browser', version, display);
});
