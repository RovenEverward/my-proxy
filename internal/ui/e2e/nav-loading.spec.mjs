import { test, expect } from '@playwright/test';
import { mkdirSync } from 'node:fs';
import { startEnv, login } from './env.mjs';
let env;
const pendingRoutes = new WeakMap();
async function settleRoutes(page) {
  const pending = pendingRoutes.get(page);
  while (pending?.size) await Promise.all([...pending]);
}
test.use({ deviceScaleFactor: 2 });
test.beforeAll(async () => {
  env = await startEnv();
  for (const name of ['nav-a', 'nav-b']) await env.seed('node', 'add', name, '--address', '203.0.113.10', '--ssh-port', '1');
  await env.seed('user', 'add', 'nav-user');
  await env.seed('inbound', 'add', '--node', '1', '--tag', 'nav-in', '--protocol', 'vless', '--port', '443', '--security', 'none');
});
test.afterAll(async () => { await env?.stop(); });
// 等在途延迟桩完成再销毁请求上下文，避免页面结束时响应被释放。
test.afterEach(async ({ page }) => { await settleRoutes(page); await page.unrouteAll({ behavior: "wait" }); });
async function ready(page, hash) {
  await settleRoutes(page);
  await page.goto(env.origin + '/#' + hash);
  await expect(page.locator('html[data-loading]')).toHaveCount(0);
}
async function platform(page, mode, theme = 'light') {
  await page.addInitScript(({ mode, theme }) => {
    Object.defineProperty(navigator, 'platform', { value: mode === 'mac' ? 'MacIntel' : 'Win32' });
    Object.defineProperty(navigator, 'userAgentData', { value: { platform: mode === 'mac' ? 'macOS' : 'Windows' } });
    localStorage.setItem('mpx-theme', theme);
    if (mode === 'win') document.addEventListener('DOMContentLoaded', () => {
      document.documentElement.style.setProperty('--mygo-titlebar-inset-right', '138px');
      document.documentElement.style.setProperty('--mygo-titlebar-height', '52px');
    });
  }, { mode, theme });
  await page.route('**/ui/meta', async r => {
    const response = await r.fetch(), meta = await response.json();
    await r.fulfill({ response, json: { ...meta, server: 'https://203.0.113.10:8080' } });
  });
}
async function delay(page) {
  let n = 0;
  const pending = new Set(); pendingRoutes.set(page, pending);
  await page.route('**/api/**', async r => {
    const task = (async () => {
      const response = await r.fetch();
      await new Promise(resolve => setTimeout(resolve, [150, 200, 250, 300][n++ % 4]));
      await r.fulfill({ response });
    })();
    pending.add(task);
    try { await task; } finally { pending.delete(task); }
  });
}
// 从真实 click 起计时，目标标题、目标地址和 busy 必须在同一帧成立。
async function measure(page, selector, hash, title, overlay = false) {
  await page.locator(selector).first().evaluate((el, args) => {
    window.navTiming = new Promise(resolve => el.addEventListener('click', () => {
      const start = performance.now();
      const check = () => {
        const root = document.querySelector(args.overlay === 'modal' ? '#modal.open' : args.overlay ? '#drawer.open' : '#page');
        const h = root?.querySelector(args.overlay ? 'h2' : 'h1');
        const rect = h?.getBoundingClientRect();
        const busy = args.overlay ? root?.querySelector('[aria-busy=true]') : document.querySelector('main[aria-busy=true]');
        if (location.hash === '#' + args.hash && h?.textContent.includes(args.title) && rect?.top >= 0 && rect?.bottom <= innerHeight && busy) {
          resolve(performance.now() - start); observer.disconnect();
        }
      };
      const observer = new MutationObserver(check);
      observer.observe(document.documentElement, { subtree: true, childList: true, attributes: true });
      requestAnimationFrame(check);
      setTimeout(() => { observer.disconnect(); resolve(null); }, 3000);
    }, { once: true, capture: true }));
  }, { hash, title, overlay });
  await page.locator(selector).first().click();
  const ms = await page.evaluate(() => window.navTiming);
  console.log('NAV_LOADING ' + JSON.stringify({ selector, hash, ms }));
  expect(ms).not.toBeNull(); expect(ms).toBeLessThan(50);
  return ms;
}
for (const mode of ['mac', 'win']) test(`${mode} · 全站路由与详情立即切换`, async ({ page }) => {
  test.setTimeout(90_000);
  await platform(page, mode); await delay(page); await login(page, env);
  for (const [hash, title] of [['nodes','节点'], ['users','用户'], ['sub','订阅'], ['rules','分流'], ['doctor','体检'], ['system','系统'], ['overview','总览']]) {
    await measure(page, `.side a[href="#${hash}"]`, hash, title);
    await expect(page.locator('html[data-loading]')).toHaveCount(0);
  }
  await ready(page, 'nodes');
  await measure(page, 'tr[data-href="#nodes/1"]', 'nodes/1', 'nav-a');
  await expect(page.locator('html[data-loading]')).toHaveCount(0);
  await measure(page, '.crumb a[href="#nodes"]', 'nodes', '节点');
  await expect(page.locator('html[data-loading]')).toHaveCount(0);
  await ready(page, 'users');
  await measure(page, 'tr[data-href="#users/nav-user"]', 'users/nav-user', 'nav-user', true);
  await expect(page.locator('html[data-loading]')).toHaveCount(0);
  await page.goBack();
  await expect(page).toHaveURL(/#users$/); await expect(page.locator('#drawer.open')).toHaveCount(0);
  await expect(page.locator('html[data-loading]')).toHaveCount(0);
  await measure(page, 'a[href="#users/plans"]', 'users/plans', '用户');
  await expect(page.locator('html[data-loading]')).toHaveCount(0);
  await ready(page, 'system');
  for (const tab of ['devices', 'backup']) {
    await measure(page, `a[href="#system/${tab}"]`, 'system/' + tab, '系统');
    await expect(page.locator('html[data-loading]')).toHaveCount(0);
  }
});
test('失败留在目标页可重试，乱序响应与关闭抽屉不会回跳', async ({ page }) => {
  await platform(page, 'mac'); await login(page, env); await ready(page, 'nodes');
  let fail = true;
  await page.route('**/api/nodes/1', async r => {
    if (fail) return r.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ error: '测试暂不可用' }) });
    await new Promise(resolve => setTimeout(resolve, 300)); await r.continue();
  });
  await page.click('tr[data-href="#nodes/1"]');
  await expect(page.locator('#page h1')).toHaveText('nav-a');
  await expect(page.locator('#page [role=alert]')).toBeVisible();
  await expect(page).toHaveURL(/#nodes\/1$/);
  fail = false; const oldResponse = page.waitForResponse(r => new URL(r.url()).pathname === '/api/nodes/1');
  await page.click('#page [data-act=reload]');
  await page.evaluate(() => { location.hash = 'nodes/2'; });
  await expect(page.locator('#page h1')).toContainText('nav-b');
  await expect(page.locator('#page h1')).not.toContainText('nav-a');
  await oldResponse;
  await expect(page.locator('html[data-loading]')).toHaveCount(0);
  await expect(page.locator('#page h1')).toContainText('nav-b');
  await expect(page.locator('#page h1')).not.toContainText('nav-a');
  await ready(page, 'users'); await delay(page);
  await page.click('tr[data-href="#users/nav-user"]');
  await page.click('#drawer [data-act=close]');
  await expect(page).toHaveURL(/#users$/); await expect(page.locator('html[data-loading]')).toHaveCount(0);
  await expect(page.locator('#drawer.open')).toHaveCount(0);
});
test('用户详情读取失败后关闭，恢复用户列表而非错误页', async ({ page }) => {
  await platform(page, 'mac'); await login(page, env); await ready(page, 'users');
  await page.route('**/api/users/nav-user', r => r.fulfill({ status: 404, json: { error: '详情读取失败测试' } }));
  await page.click('tr[data-href="#users/nav-user"]');
  await expect(page.locator('#drawer.open [role=alert]')).toBeVisible();
  await page.click('#drawer [data-act=close]');
  await expect(page).toHaveURL(/#users$/);
  await expect(page.locator('html[data-loading]')).toHaveCount(0);
  await expect(page.locator('tr[data-href="#users/nav-user"]')).toBeVisible();
  await expect(page.locator('#page [role=alert]')).toHaveCount(0);
});
test('抽屉先显示目标，读取失败可重试；关闭后不复活', async ({ page }) => {
  await platform(page, 'mac'); await delay(page); await login(page, env); await ready(page, 'users');
  await measure(page, '[data-act=user-new]', 'users', '添加用户', true);
  await page.click('#drawer [data-act=close]');
  await page.waitForResponse(r => new URL(r.url()).pathname === '/api/plans');
  await expect(page.locator('#drawer.open')).toHaveCount(0);
  await expect(page.locator('[data-act=user-new]')).not.toHaveAttribute('aria-busy', 'true');
  await page.click('[data-act=user-new]'); await expect(page.locator('#user-form')).toBeVisible();
});
test('编辑、连接、分流和底稿抽屉先显示框架，错误留在原框重试', async ({ page }) => {
  test.setTimeout(60_000);
  await platform(page, 'mac'); await delay(page); await login(page, env);
  await ready(page, 'nodes/1');
  await page.click('[data-act=node-menu]');
  await measure(page, '[data-act=node-edit]', 'nodes/1', '编辑', true);
  await expect(page.locator('#node-form')).toBeVisible(); await page.click('#node-form [data-act=close]');
  await measure(page, '.cx-row[data-act=conn-edit]', 'nodes/1', '', true);
  await expect(page.locator('#conn-form')).toBeVisible();
  await page.click('#conn-form summary');
  await measure(page, '[data-act=conn-host-new]', 'nodes/1', '添加对外地址', true);
  await expect(page.locator('#host-form')).toBeVisible(); await page.click('#host-form [data-act=close]');
  await measure(page, '[data-act=node-config]', 'nodes/1', '配置预览', true);
  await expect(page.locator('#drawer [aria-busy=true]')).toHaveCount(0); await page.click('#drawer [data-act=close]');
  await ready(page, 'rules/groups');
  await measure(page, '[data-act=gp-edit][data-id=""]', 'rules/groups', '新建策略组', true);
  await expect(page.locator('#gp-form')).toBeVisible(); await page.click('#gp-form [data-act=close]');
  await ready(page, 'sub/templates');
  await measure(page, '[data-act=tpl-open][data-f="sing-box"]', 'sub/templates', 'sing-box', true);
  await expect(page.locator('#tpl-form')).toBeVisible(); await page.click('#tpl-form [data-act=close]');
  await ready(page, 'users/nav-user');
  await measure(page, '[data-act=user-edit]', 'users/nav-user', '编辑 nav-user', true);
  await expect(page.locator('#user-form')).toBeVisible();
  await page.evaluate(() => { location.hash = 'users/plans'; });
  await expect(page.locator('#drawer.open')).toHaveCount(0);
  await expect(page.locator('html[data-loading]')).toHaveCount(0);
  await ready(page, 'users');
  let fail = true;
  await page.route('**/api/inbounds', r => fail ? r.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ error: '读取失败测试' }) }) : r.fallback());
  await page.click('[data-act=user-new]');
  await expect(page.locator('#drawer [role=alert]')).toBeVisible();
  await expect(page.locator('#drawer h2')).toHaveText('添加用户');
  fail = false; await page.click('#drawer [data-act=reload]');
  await expect(page.locator('#user-form')).toBeVisible();
});
test('长列表进入详情归零滚动，返回列表不继承详情偏移', async ({ page }) => {
  await platform(page, 'mac'); await login(page, env);
  await page.route('**/api/nodes', async r => {
    const response = await r.fetch(), rows = await response.json();
    await r.fulfill({ response, json: Array.from({ length: 40 }, (_, i) => ({ ...rows[0], id: i + 1, name: 'nav-' + (i + 1) })) });
  });
  await ready(page, 'nodes'); await delay(page);
  await page.locator('main').evaluate(el => { el.scrollTop = el.scrollHeight; });
  await measure(page, 'tr[data-href="#nodes/40"]', 'nodes/40', 'nav-40');
  expect(await page.locator('main').evaluate(el => el.scrollTop)).toBe(0);
  await page.goBack(); await expect(page.locator('html[data-loading]')).toHaveCount(0);
  expect(await page.locator('main').evaluate(el => el.scrollTop)).toBe(0);
});
test('控制面列表与切换立即显示目标，失败可原地重试', async ({ page }) => {
  await platform(page, 'mac');
  await page.route('**/local/profiles', async r => {
    await new Promise(resolve => setTimeout(resolve, 225));
    await r.fulfill({ json: [{ name: 'demo', server: 'https://203.0.113.10:8080', current: true }, { name: 'test', server: 'https://198.51.100.10:8080' }] });
  });
  await page.route('**/local/profiles/use', async r => {
    await new Promise(resolve => setTimeout(resolve, 225));
    await r.fulfill({ status: 503, json: { error: '切换失败测试' } });
  });
  await login(page, env); await ready(page, 'nodes');
  await page.click('.profile[data-act=cp-toggle]');
  await measure(page, '[data-act=cp-switch]', 'nodes', '切换控制面', 'modal');
  await expect(page.locator('.pfitem')).toHaveCount(2);
  await measure(page, '.pfitem[data-name=test]', 'nodes', '正在切换到 test', 'modal');
  await expect(page.locator('#prof-err')).toHaveText('切换失败测试');
  await expect(page.locator('#profile-retry')).toBeVisible();
});
test('分流冷启动与标签切换的加载框架保留标签和规则作用域', async ({ page }) => {
  await platform(page, 'mac'); await delay(page); await login(page, env);
  let release;
  let gate = new Promise(resolve => { release = resolve; });
  await page.route('**/api/proxy-groups/preview', async r => { await gate; await r.continue(); });
  await page.goto(env.origin + '/#rules/groups');
  await expect(page.locator('main[aria-busy=true] .sk').first()).toBeVisible();
  await expect(page.locator('[data-act=rp-tab][data-k=groups].on')).toBeVisible();
  release(); await expect(page.locator('html[data-loading]')).toHaveCount(0);
  await page.evaluate(() => { location.hash = 'rules/rules'; });
  await expect(page.locator('main[aria-busy=true] #rp-scope')).toBeVisible();
  await expect(page.locator('[data-act=rp-tab][data-k=rules].on')).toBeVisible();
  await expect(page.locator('html[data-loading]')).toHaveCount(0);
  gate = new Promise(resolve => { release = resolve; });
  await page.click('[data-act=rp-tab][data-k=groups]');
  await expect(page.locator('main[aria-busy=true] .sk').first()).toBeVisible();
  await expect(page.locator('[data-act=rp-tab][data-k=groups].on')).toBeVisible();
  release(); await expect(page.locator('.rp-g').first()).toBeVisible();
  await expect(page.locator('html[data-loading]')).toHaveCount(0);
});
test('后台轮询失败只提示，手动刷新失败才显示页面重试', async ({ page }) => {
  await platform(page, 'mac'); await login(page, env); await ready(page, 'users');
  await page.route('**/api/users?**', r => r.fulfill({ status: 503, json: { error: '轮询失败测试' } }));
  // 与 30s 轮询调用同一入口，不推进其他定时任务。
  await page.evaluate(() => load(true));
  await expect(page.locator('tr[data-href="#users/nav-user"]')).toBeVisible();
  await expect(page.locator('#page [role=alert]')).toHaveCount(0);
  await expect(page.locator('.toast.bad')).toContainText('轮询失败测试');
  await page.click('.top [data-act=reload]');
  await expect(page.locator('#page [role=alert]')).toContainText('轮询失败测试');
  await expect(page.locator('tr[data-href="#users/nav-user"]')).toBeVisible();
});
for (const mode of ['mac', 'win']) for (const theme of ['light', 'dark']) test(`${mode}/${theme} · 加载与完成截图`, async ({ page }) => {
  test.skip(!process.env.MPX_NAV_LOADING_SHOTS, '截图按需生成');
  await platform(page, mode, theme); await login(page, env); await ready(page, 'nodes');
  let release;
  const held = new Promise(resolve => { release = resolve; });
  await page.route('**/api/nodes/1', async r => { await held; await r.continue(); });
  await page.click('tr[data-href="#nodes/1"]');
  await expect(page.locator('main[aria-busy=true] h1')).toHaveText('nav-a');
  const dir = process.env.MPX_NAV_LOADING_SHOTS; mkdirSync(dir, { recursive: true });
  await page.screenshot({ path: `${dir}/${mode}-${theme}-loading.png` });
  release(); await expect(page.locator('html[data-loading]')).toHaveCount(0);
  await page.screenshot({ path: `${dir}/${mode}-${theme}-loaded.png` });
});
