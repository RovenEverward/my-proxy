import { test, expect } from '@playwright/test';
import { appendFileSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { startEnv, login } from './env.mjs';
let env;
test.beforeAll(async () => {
  env = await startEnv();
  await env.seed('node', 'add', 'latency-node', '--address', '203.0.113.10', '--ssh-port', '1');
  await env.seed('user', 'add', 'latency-user');
});
test.afterAll(async () => { await env?.stop(); });
// 同一测试环境可重放改前脚本，计时方法和网络延迟与修后完全相同。
test.beforeEach(async ({ page }) => {
  const base = process.env.MPX_LATENCY_BASELINE;
  if (!base || base === '1') return;
  const names = new Set(['app.js','rules.js','users.js','plans.js','settings.js','nodes.js','conn.js']);
  await page.route('**/*.js', route => {
    const name = new URL(route.request().url()).pathname.split('/').pop();
    if (!names.has(name)) return route.continue();
    const body = execFileSync('git', ['show', `${base}:internal/ui/assets/${name}`], { encoding: 'utf8' });
    return route.fulfill({ contentType: 'application/javascript', body });
  });
});


async function measure(page, name, selector, feedback, result) {
  const requests = [];
  const startRequest = request => {
    if (!new URL(request.url()).pathname.startsWith('/api/')) return;
    requests.push({ request, method: request.method(), path: new URL(request.url()).pathname, start: performance.now() });
  };
  const endRequest = request => {
    const row = requests.find(r => r.request === request);
    if (row) row.duration = performance.now() - row.start;
  };
  page.on('request', startRequest); page.on('requestfinished', endRequest);
  await page.locator(selector).evaluate((el, { feedback, result }) => {
    window.latencyTiming = { feedback: null, result: null };
    window.latencyDone = new Promise(resolve => el.addEventListener('click', () => {
      const start = performance.now();
      const visible = selector => [...document.querySelectorAll(selector)].some(node => node.getClientRects().length && getComputedStyle(node).visibility !== 'hidden');
      const check = () => {
        if (visible(feedback + ',.ui-progress')) window.latencyTiming.feedback ??= performance.now() - start;
        if (visible(result) && !document.documentElement.hasAttribute('data-loading')) window.latencyTiming.result ??= performance.now() - start;
        if (window.latencyTiming.feedback !== null && window.latencyTiming.result !== null) { observer.disconnect(); resolve(window.latencyTiming); }
      };
      const observer = new MutationObserver(check);
      observer.observe(document.documentElement, { subtree: true, attributes: true, childList: true, characterData: true });
      setTimeout(() => { observer.disconnect(); resolve(window.latencyTiming); }, 5000);
    }, { capture: true, once: true }));
  }, { feedback, result });
  await page.locator(selector).click();
  const timing = await page.evaluate(() => window.latencyDone);
  await expect(page.locator(result).first()).toBeVisible();
  await expect(page.locator('html[data-loading]')).toHaveCount(0);
  page.off('request', startRequest); page.off('requestfinished', endRequest);
  const row = { name, ...timing, requests: requests.map(({request,...r}) => ({...r,start:undefined})) };
  console.log('LATENCY ' + JSON.stringify(row));
  if (process.env.MPX_LATENCY_RESULT) appendFileSync(process.env.MPX_LATENCY_RESULT, JSON.stringify(row)+'\n');
  if (!process.env.MPX_LATENCY_BASELINE) {
    expect(timing.feedback).not.toBeNull();
    expect(timing.feedback).toBeLessThan(50);
  }
  return row;
}

test('延迟网络下主要切换与抽屉立即反馈', async ({ page }) => {
  test.setTimeout(60_000);
  await page.route('**/api/**', async route => {
    const response = await route.fetch();
    await new Promise(resolve => setTimeout(resolve, 225));
    await route.fulfill({ response });
  });
  await login(page, env);
  await page.goto(env.origin + '/#rules');
  await expect(page.locator('html[data-loading]')).toHaveCount(0);
  await measure(page, '分流→策略组', '[data-act=rp-tab][data-k=groups]', '[data-act=rp-tab][data-k=groups].on, [data-act=rp-tab][data-k=groups][aria-busy=true]', '.rp-g');
  await measure(page, '策略组编辑抽屉', '[data-act=gp-edit][data-id=""]', '#drawer.open, [data-act=gp-edit][aria-busy=true]', '#gp-pv > *');
  await page.click('#gp-form [data-act=close]');
  await measure(page, '策略组→规则', '[data-act=rp-tab][data-k=rules]', '[data-act=rp-tab][data-k=rules].on, [data-act=rp-tab][data-k=rules][aria-busy=true]', '.rp-row');
  await page.goto(env.origin + '/#users');
  await expect(page.locator('html[data-loading]')).toHaveCount(0);
  await measure(page, '用户状态筛选', '[data-act=user-filter][data-status=active]', '[data-act=user-filter][data-status=active].on, [data-act=user-filter][aria-busy=true]', '[data-act=user-filter][data-status=active].on');
  await measure(page, '新建用户抽屉', '[data-act=user-new]', '#drawer.open, [data-act=user-new][aria-busy=true]', '#user-form');
  await page.click('#drawer [data-act=close]');
  await page.goto(env.origin + '/#nodes');
  await expect(page.locator('html[data-loading]')).toHaveCount(0);
  await page.goto(env.origin + '/#nodes/1');
  await expect(page.locator('html[data-loading]')).toHaveCount(0);
  await page.click('[data-act=node-menu]');
  await measure(page, '编辑节点抽屉', '[data-act=node-edit]', '#drawer.open, [data-act=node-edit][aria-busy=true]', '#node-form');
});

test('手动刷新后的新列表不会被筛选快照覆盖，用户计数随写操作更新', async ({ page }) => {
  await login(page, env);
  await page.goto(env.origin + '/#users');
  await expect(page.locator('html[data-loading]')).toHaveCount(0);
  await env.seed('user', 'add', 'fresh-after-poll');
  await page.click('[data-act=reload]');
  await expect(page.locator('tr[data-href="#users/fresh-after-poll"]')).toHaveCount(1);
  await page.click('[data-act=user-filter][data-status=active]');
  await expect(page.locator('html[data-loading]')).toHaveCount(0);
  await expect(page.locator('tr[data-href="#users/fresh-after-poll"]')).toHaveCount(1);
  await page.click('tr[data-href="#users/fresh-after-poll"]');
  await page.click('[data-act=user-menu]');
  await page.click('[data-act=user-toggle]');
  await page.click('#okb');
  await expect(page.locator('.toast')).toContainText('已停用');
  await page.keyboard.press('Escape');
  await expect(page.locator('[data-act=user-filter][data-status=disabled] em')).toHaveText('1');
  await page.click('[data-act=user-filter][data-status=disabled]');
  await expect(page.locator('tr[data-href="#users/fresh-after-poll"]')).toContainText('已停用');
});

test('规则作用域不串用快照，失败时恢复原选择与反馈', async ({ page }) => {
  await env.seed('rule', '--user', 'latency-user', 'add', 'keyword', 'scope-only', '直连');
  await login(page, env);
  await page.goto(env.origin + '/#rules');
  await expect(page.locator('html[data-loading]')).toHaveCount(0);
  await page.selectOption('#rp-scope', 'latency-user');
  await expect(page.locator('.rp-row.own')).toContainText('scope-only');
  await page.selectOption('#rp-scope', '');
  await expect(page.locator('html[data-loading]')).toHaveCount(0);
  await expect(page.locator('.rp-row.own')).toHaveCount(0);
  await page.route('**/api/rules?user=latency-user', async route => {
    await new Promise(resolve => setTimeout(resolve, 225));
    await route.fulfill({ status: 500, json: { error: '测试拒绝作用域读取' } });
  });
  await page.selectOption('#rp-scope', 'latency-user');
  await expect(page.locator('.toast')).toContainText('测试拒绝');
  await expect(page.locator('#rp-scope')).toHaveValue('');
  await expect(page.locator('#ui-progress')).toHaveCount(0);
});

test('单条规则写后只读取规则资源，兜底写失败恢复原值', async ({ page }) => {
  await login(page, env);
  await page.goto(env.origin + '/#rules');
  await expect(page.locator('html[data-loading]')).toHaveCount(0);
  const reads = [];
  page.on('request', request => { if (request.method() === 'GET' && new URL(request.url()).pathname.startsWith('/api/')) reads.push(new URL(request.url()).pathname); });
  await page.locator('[data-act=rp-toggle]').first().click();
  await expect(page.locator('.rp-row.off').first()).toBeVisible();
  expect(reads).toEqual(['/api/rules']);
  const original = await page.locator('#rp-final').inputValue();
  await page.route('**/api/rules/final', async route => {
    await new Promise(resolve => setTimeout(resolve, 225));
    await route.fulfill({ status: 500, json: { error: '测试拒绝兜底修改' } });
  });
  await page.selectOption('#rp-final', original === 'DIRECT' ? 'REJECT' : 'DIRECT');
  await expect(page.locator('.toast')).toContainText('测试拒绝');
  await expect(page.locator('#rp-final')).toHaveValue(original);
});

test('进入页面时读取控制面列表不取消页面加载', async ({ page }) => {
  await login(page, env);
  await page.route('**/api/rules', async route => {
    const response = await route.fetch();
    await new Promise(resolve => setTimeout(resolve, 300));
    await route.fulfill({ response });
  });
  await page.goto(env.origin + '/#rules');
  await expect(page.locator('html[data-loading]')).toHaveCount(1);
  await page.evaluate(() => api('/local/profiles', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' }).catch(() => {}));
  await expect(page.locator('.rp-row').first()).toBeVisible();
  await expect(page.locator('html[data-loading]')).toHaveCount(0);
});

test('连续切筛选与作用域不复用前一选择的在途请求', async ({ page }) => {
  await login(page, env);
  await page.goto(env.origin + '/#users');
  await expect(page.locator('html[data-loading]')).toHaveCount(0);
  await page.route('**/api/users?*', async route => {
    const response = await route.fetch();
    const slow = new URL(route.request().url()).searchParams.get('status') === 'active';
    await new Promise(resolve => setTimeout(resolve, slow ? 300 : 150));
    await route.fulfill({ response });
  });
  await page.click('[data-act=user-filter][data-status=active]');
  await page.click('[data-act=user-filter][data-status=disabled]');
  await expect(page.locator('html[data-loading]')).toHaveCount(0);
  await expect(page.locator('[data-act=user-filter][data-status=disabled]')).toHaveClass(/on/);
  await expect(page.locator('tr[data-href="#users/latency-user"]')).toHaveCount(0);
  await expect(page.locator('tr[data-href="#users/fresh-after-poll"]')).toContainText('已停用');
});
