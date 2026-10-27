import { test, expect } from '@playwright/test';
import { mkdirSync } from 'node:fs';
import { startEnv, login, cpFetch } from './env.mjs';
let env;
const cleanup = new WeakMap();
test.use({ deviceScaleFactor: 2 });
test.beforeAll(async () => {
  env = await startEnv();
  await env.seed('node', 'add', 'reuse-node', '--address', '203.0.113.10', '--ssh-port', '1');
  await env.seed('node', 'add', 'reuse-node-b', '--address', '198.51.100.10', '--ssh-port', '1');
  for (const name of ['reuse-a', 'reuse-b']) await env.seed('user', 'add', name);
});
test.afterAll(async () => { await env?.stop(); });
test.beforeEach(async ({ page }) => {
  cleanup.set(page, []);
  await page.addInitScript(() => {
    Object.defineProperty(navigator, 'platform', { value: 'MacIntel' });
    Object.defineProperty(navigator, 'userAgentData', { value: { platform: 'macOS' } });
  });
});
test.afterEach(async ({ page }) => {
  for (const release of cleanup.get(page)) release();
  await page.unrouteAll({ behavior: 'wait' });
});
async function ready(page, hash = 'users') {
  await page.goto(env.origin + '/#' + hash);
  await expect(page.locator('html[data-loading]')).toHaveCount(0);
}
async function lag(page) {
  await page.route('**/api/**', async r => {
    const response = await r.fetch();
    await new Promise(resolve => setTimeout(resolve, 225));
    await r.fulfill({ response });
  });
}
async function gate(page, pattern) {
  let release;
  const held = new Promise(resolve => { release = resolve; });
  cleanup.get(page).push(release);
  await page.route(pattern, async r => { await held; await r.fallback(); });
  return release;
}
async function detailReady(page) { await expect(page.locator('#drawer .ud-b[aria-busy]')).toHaveCount(0); }

test('用户详情局部补全，打开、换人、关闭和后退保留同一列表、筛选与滚动', async ({ page }) => {
  await login(page, env); await ready(page);
  await page.route('**/api/users?**', async r => {
    const response = await r.fetch(), data = await response.json();
    const extras = Array.from({ length: 30 }, (_, i) => ({ ...data.users[0], username: 'extra-' + i }));
    await r.fulfill({ response, json: { ...data, users: [...data.users, ...extras], total: 32 } });
  });
  await page.click('[data-act=user-filter][data-status=active]');
  await expect(page.locator('tr[data-href="#users/extra-29"]')).toHaveCount(1);
  await page.evaluate(() => {
    window.baseList = document.querySelector('#page tbody');
    document.querySelector('main').scrollTop = 140;
    window.baseY = document.querySelector('main').scrollTop;
  });
  await lag(page);
  const paths = [];
  page.on('request', r => { if (new URL(r.url()).pathname.startsWith('/api/')) paths.push(new URL(r.url()).pathname + new URL(r.url()).search); });
  await page.locator('tr[data-href="#users/reuse-a"]').evaluate(el => {
    window.openTime = new Promise(resolve => el.addEventListener('click', () => {
      const start = performance.now();
      queueMicrotask(() => resolve(document.querySelector("#drawer.open .ud-t h2")?.textContent === "reuse-a" && document.querySelector("#drawer .ud-use") ? performance.now() - start : null));
    }, { once: true, capture: true }));
    el.click();
  });
  const feedback = await page.evaluate(() => window.openTime);
  expect(feedback).not.toBeNull(); expect(feedback).toBeLessThan(50);
  console.log("DATA_REUSE_FEEDBACK " + feedback);
  await expect(page.locator('#drawer .ud-t h2')).toHaveText('reuse-a');
  await expect(page.locator('#drawer .ud-use')).toBeVisible();
  await expect(page.locator('#send[aria-busy=true]')).toBeVisible();
  await expect(page.locator('#drawer')).not.toContainText('正在加载…');
  await expect(page.locator('#page .sk,.ui-progress')).toHaveCount(0);
  await expect(page.locator('.crumb')).toHaveText('用户/reuse-a');
  await detailReady(page);
  expect(paths.sort()).toEqual(['/api/settings', '/api/users/reuse-a/activity', '/api/users/reuse-a/links']);
  paths.length = 0;
  await page.evaluate(() => followRoute('#users/reuse-b'));
  await expect(page.locator('.crumb')).toHaveText('用户/reuse-b'); await detailReady(page);
  await page.goBack(); await expect(page.locator('.crumb')).toHaveText('用户/reuse-a'); await detailReady(page);
  paths.length = 0;
  await page.click('#drawer [data-act=close]');
  await expect(page).toHaveURL(/#users$/);
  await expect(page.locator('.crumb')).toHaveText('用户');
  expect(paths).toEqual([]);
  expect(await page.evaluate(() => window.baseList === document.querySelector('#page tbody'))).toBe(true);
  expect(await page.locator('main').evaluate(el => el.scrollTop)).toBe(await page.evaluate(() => window.baseY));
  await expect(page.locator('[data-act=user-filter][data-status=active]')).toHaveClass(/on/);
});

test('详情乱序与局部失败只影响对应区域，关闭后旧响应不复活', async ({ page }) => {
  await login(page, env); await ready(page);
  const release = await gate(page, '**/api/users/reuse-a/**');
  await page.click('tr[data-href="#users/reuse-a"]');
  await page.evaluate(() => followRoute('#users/reuse-b'));
  await expect(page.locator('#drawer h2')).toHaveText('reuse-b'); await detailReady(page);
  release();
  await expect(page.locator('#drawer h2')).toHaveText('reuse-b');
  await page.click('#drawer [data-act=close]');
  let fail = true;
  await page.route('**/api/users/reuse-a/links', r => fail ? r.fulfill({ status: 503, json: { error: '链接读取失败测试' } }) : r.fallback());
  await page.click('tr[data-href="#users/reuse-a"]');
  await expect(page.locator('#send [role=alert]')).toContainText('链接读取失败测试');
  await expect(page.locator('#drawer .ud-use')).toBeVisible();
  fail = false; await page.click('#send [data-act=user-detail-retry]');
  await expect(page.locator('#send .send-link')).toBeVisible();
  await page.click('#drawer [data-act=close]');
  await expect(page.locator('#drawer.open')).toHaveCount(0);
});

test('返回缓存立即显示，后台失败只提示，筛选与分流读取缓存不串用', async ({ page }) => {
  await login(page, env); await ready(page);
  await page.click('[data-act=user-filter][data-status=disabled]');
  await expect(page.locator('html[data-loading]')).toHaveCount(0);
  await page.click('.side a[href="#nodes"]'); await expect(page.locator('html[data-loading]')).toHaveCount(0);
  await page.route('**/api/users?**', async r => { await new Promise(resolve => setTimeout(resolve, 225)); await r.fulfill({ status: 503, json: { error: '后台读取失败测试' } }); });
  await page.click('.side a[href="#users"]');
  await expect(page.locator('[data-act=user-filter][data-status=disabled].on')).toBeVisible();
  await expect(page.locator('#page .sk')).toHaveCount(0);
  await expect(page.locator('.toast.bad')).toContainText('后台读取失败测试');
  await expect(page.locator('#page [role=alert]')).toHaveCount(0);
  await page.unroute('**/api/users?**');
  await page.evaluate(() => followRoute('#rules/rules')); await expect(page.locator('html[data-loading]')).toHaveCount(0);
  await page.click('.side a[href="#nodes"]'); await expect(page.locator('html[data-loading]')).toHaveCount(0);
  await lag(page);
  await page.evaluate(() => followRoute('#rules/groups'));
  await expect(page.locator('[data-act=rp-tab][data-k=groups].on')).toBeVisible();
  // rules/rules 的缓存不能冒充含 members 的 groups 页面。
  await expect(page.locator('main[aria-busy=true]')).toHaveCount(1);
  await expect(page.locator('html[data-loading]')).toHaveCount(0);
  await expect(page.locator('.rp-g').first()).toBeVisible();
});

test('缓存详情不在当前列表时仍补读用户，缓存链接失败保留内容并提示', async ({ page }) => {
  await login(page, env); await ready(page);
  await page.click('tr[data-href="#users/reuse-a"]'); await detailReady(page);
  await page.click('#drawer [data-act=close]');
  await page.fill('#user-search', 'reuse-b');
  await expect(page.locator('tr[data-href="#users/reuse-a"]')).toHaveCount(0);
  await page.route('**/api/users/reuse-a', async r => {
    const response = await r.fetch(), user = await response.json();
    await r.fulfill({ response, json: { ...user, enabled: false, status: 'disabled' } });
  });
  const release = await gate(page, '**/api/users/reuse-a');
  await page.route('**/api/users/reuse-a/links', r => r.fulfill({ status: 503, json: { error: '缓存链接刷新失败测试' } }));
  const request = page.waitForRequest(r => new URL(r.url()).pathname === '/api/users/reuse-a');
  await page.evaluate(() => followRoute('#users/reuse-a'));
  await request;
  await expect(page.locator('#drawer .ud-use')).toBeVisible();
  await expect(page.locator('#send .send-link')).toBeVisible();
  await expect(page.locator('#send [role=alert]')).toContainText('缓存链接刷新失败测试');
  release();
  await expect(page.locator('#drawer .ud-t')).toContainText('停用');
  await page.click('[data-act=user-menu]');
  await expect(page.locator('[data-act=user-toggle]')).toHaveAttribute('data-on', '');
});

test('列表后台读取在叠层期间完成不更换数据与 DOM，关闭也不补画', async ({ page }) => {
  await login(page, env); await ready(page);
  await page.evaluate(() => { window.baseList = document.querySelector('#page tbody'); window.baseData = current.data; });
  const release = await gate(page, '**/api/users?**');
  await page.evaluate(() => { window.pollDone = load(true); });
  await page.click('tr[data-href="#users/reuse-a"]');
  release(); await page.evaluate(() => window.pollDone); await detailReady(page);
  expect(await page.evaluate(() => current.data === window.baseData && window.baseList === document.querySelector('#page tbody'))).toBe(true);
  await page.click('#drawer [data-act=close]');
  expect(await page.evaluate(() => window.baseList === document.querySelector('#page tbody'))).toBe(true);
});

test('续期并清零成功后详情的到期日和剩余用量一起更新', async ({ page }) => {
  const headers = { Authorization: 'Bearer ' + env.token, 'Content-Type': 'application/json' };
  const response = await cpFetch(env.cpURL + '/api/users/reuse-b', { headers });
  const user = await response.json(), expires = new Date(); expires.setDate(expires.getDate() + 2);
  const saved = await cpFetch(env.cpURL + '/api/users/reuse-b', { method: 'PUT', headers,
    body: JSON.stringify({ ...user, expire_at: expires.toISOString(), data_limit: 2 ** 30, used_traffic: 2 ** 31 }) });
  expect(saved.ok).toBe(true);
  await login(page, env); await ready(page);
  await page.click('tr[data-href="#users/reuse-b"]'); await detailReady(page);
  await expect(page.locator('#drawer .ud-v')).toContainText('超出');
  await page.click('[data-act=user-menu]'); await page.click('[data-act=user-renew]');
  await page.click('[data-rn="90"]');
  const target = await page.locator('#rn-to b').textContent();
  await page.check('#rn-reset'); await page.click('#okb');
  await expect(page.locator('#modal.open')).toHaveCount(0);
  await expect(page.locator('#drawer .ud-kv dt:has-text("到期") + dd')).toContainText(target);
  await expect(page.locator('#drawer .ud-v')).not.toContainText('超出');
  await expect(page.locator('#drawer .bar i')).toHaveAttribute('style', 'width:100.0%');
});

test('写失败后详情仍可重试，不提交写前的补全响应', async ({ page }) => {
  await login(page, env); await ready(page);
  const release = await gate(page, '**/api/users/reuse-a/links');
  await page.route('**/api/users/reuse-a/reset-traffic', r => r.fulfill({ status: 503, json: { error: '清零失败测试' } }));
  await page.click('tr[data-href="#users/reuse-a"]');
  await page.click('[data-act=user-menu]'); await page.click('[data-act=user-reset]');
  await page.click('#okb');
  await expect(page.locator('#modal')).toContainText('清零失败测试');
  await page.click('#modal [data-act=modal-cancel]');
  release();
  await expect(page.locator('#send [role=alert]')).toContainText('资料在读取时有变动');
  await page.click('#send [data-act=user-detail-retry]');
  await expect(page.locator('#send .send-link')).toBeVisible();
  await expect(page.locator('#send [role=alert]')).toHaveCount(0);
});

test('节点首帧的添加连接等待当前节点数据，不使用前一台缓存', async ({ page }) => {
  await login(page, env); await ready(page, 'nodes/1');
  await page.evaluate(() => followRoute('#nodes')); await expect(page.locator('html[data-loading]')).toHaveCount(0);
  const release = await gate(page, '**/api/nodes/2');
  await page.click('tr[data-href="#nodes/2"]');
  await expect(page.locator('#page h1')).toContainText('reuse-node-b');
  const add = page.locator('[data-act=conn-new]').first();
  await expect(add).toHaveAttribute('aria-disabled', 'true');
  await expect(add).toHaveAttribute('aria-describedby', 'conn-loading-why');
  await add.evaluate(el => el.click());
  await expect(page.locator('#drawer.open')).toHaveCount(0);
  release(); await expect(page.locator('html[data-loading]')).toHaveCount(0);
  await expect(add).not.toHaveAttribute('aria-disabled', 'true');
  await add.click();
  await expect(page.locator('#a-node')).toHaveValue('2');
  await page.click('#drawer [data-act=close]');
  const releaseFirst = await gate(page, '**/api/nodes/1');
  await page.evaluate(() => followRoute('#nodes/1'));
  // 已访问详情的缓存也必须同步编辑器上下文，不能残留节点 2。
  await add.click(); await expect(page.locator('#a-node')).toHaveValue('1');
  await page.click('#drawer [data-act=close]');
  releaseFirst();
});

test('经 mpx ui 的真实管理 JSON 使用 gzip，二维码不再重读链接，订阅页并行请求状态', async ({ page }) => {
  await login(page, env); await ready(page);
  const data = await page.evaluate(async () => { const r = await fetch('/api/rule-catalog'); return { encoding: r.headers.get('content-encoding'), data: await r.json() }; });
  expect(data.encoding).toBe('gzip'); expect(data.data).toBeTruthy();
  let links = 0;
  page.on('request', r => { if (new URL(r.url()).pathname === '/api/users/reuse-a/links') links++; });
  await page.click('tr[data-href="#users/reuse-a"]'); await detailReady(page);
  await expect.poll(() => page.locator('.qrs').evaluate(async img => { await img.decode(); return img.naturalWidth; })).toBeGreaterThan(0);
  expect(links).toBe(1);
  await page.click('#drawer [data-act=close]');
  const release = await gate(page, '**/api/status');
  const settings = page.waitForRequest(r => new URL(r.url()).pathname === '/api/settings');
  await page.click('.side a[href="#sub"]');
  await settings; // status 仍被扣住，其他读取已经发出。
  release(); await expect(page.locator('html[data-loading]')).toHaveCount(0);
});

for (const theme of ['light', 'dark']) test(`详情复用 · ${theme} 截图`, async ({ page }) => {
  test.skip(!process.env.MPX_DATA_REUSE_SHOTS, '截图按需生成');
  await page.addInitScript(theme => localStorage.setItem('mpx-theme', theme), theme);
  await page.route('**/ui/meta', async r => { const response = await r.fetch(); await r.fulfill({ response, json: { ...(await response.json()), server: 'https://203.0.113.10:8080' } }); });
  await login(page, env); await ready(page);
  await page.route('**/api/users/reuse-a/links', async r => { const response = await r.fetch(); await r.fulfill({ response, json: { ...(await response.json()), auto: 'https://sub.example.com/sub/example' } }); });
  const release = await gate(page, '**/api/users/reuse-a/**');
  await page.click('tr[data-href="#users/reuse-a"]');
  const dir = process.env.MPX_DATA_REUSE_SHOTS; mkdirSync(dir, { recursive: true });
  await page.screenshot({ path: `${dir}/${theme}-partial.png` });
  release(); await detailReady(page);
  await page.screenshot({ path: `${dir}/${theme}-complete.png` });
});
