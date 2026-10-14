---
layout: home
title: MPX
---

<section class="wrap hero">
<div class="hero-text">
<p class="eyebrow">MPX · 自建代理控制面</p>
<h1>一台控制面，<br>管住<em>所有节点</em></h1>
<p class="lede">MPX 在一台服务器上保存节点、用户和订阅，你在自己电脑上用 <code>mpx</code> 命令行操作它。用户流量直连节点，不经过控制面。</p>
<div class="cta">
<a class="btn btn-lg" href="./tutorial/quickstart">快速开始</a>
<a class="btn-ghost" href="./tutorial/local-trial">先在本机试用 →</a>
</div>
<div class="code install"><div class="code-bar">安装命令行 · macOS / Linux<button class="copy" type="button" data-copy="install" aria-label="复制代码" title="复制"><svg class="i-copy" viewBox="0 0 16 16" aria-hidden="true"><rect x="5.5" y="5.5" width="8" height="8" rx="2"/><path d="M10.5 3.5v-.5a1.5 1.5 0 0 0-1.5-1.5H4A1.5 1.5 0 0 0 2.5 3v5A1.5 1.5 0 0 0 4 9.5h.5"/></svg><svg class="i-ok" viewBox="0 0 16 16" aria-hidden="true"><path d="M3.5 8.5l3 3 6-7"/></svg></button></div><pre id="install">curl -fsSL https://raw.githubusercontent.com/RovenEverward/my-proxy/main/deploy/install-cli.sh | bash</pre></div>
</div>
<figure class="tile hero-fig"><svg class="hl" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 400 320" role="img" aria-label="一台控制面悬在三台节点之上"><g><path class="sil" d="M59.35 177.76L59.82 175.98L61.19 174.31L63.39 172.88L157.72 125.72L160.57 124.62L163.90 123.93L167.47 123.70L171.04 123.93L174.37 124.62L177.23 125.72L336.61 205.41L338.81 206.84L340.18 208.50L340.65 210.29L340.65 216.26L340.18 218.05L338.81 219.71L336.61 221.14L242.28 268.31L239.43 269.40L236.10 270.09L232.53 270.33L228.96 270.09L225.63 269.40L222.77 268.31L63.39 188.62L61.19 187.19L59.82 185.52L59.35 183.74Z"/><path class="lo nf" d="M334.99 206.22L336.81 207.41L337.96 208.80L338.35 210.29L337.96 211.78L336.81 213.16L334.99 214.35L240.66 261.52L238.28 262.43L235.50 263.01L232.53 263.20L229.55 263.01L226.78 262.43L224.40 261.52L65.01 181.83L63.19 180.64L62.04 179.25L61.65 177.76L62.04 176.27L63.19 174.89L65.01 173.70L159.34 126.53L161.72 125.62L164.50 125.04L167.47 124.85L170.45 125.04L173.22 125.62L175.60 126.53Z"/></g><path class="dash" d="M177.2 80.9L175.6 174.6"/><path class="dash" d="M177.2 80.9L252.9 172.6"/><path class="dash" d="M177.2 80.9L224.4 207.2"/><g><path class="sil hi" d="M124.12 70.89L124.43 69.70L125.35 68.59L126.81 67.63L162.59 49.74L164.50 49.01L166.72 48.55L169.10 48.40L171.48 48.55L173.70 49.01L175.60 49.74L227.65 75.77L229.11 76.72L230.03 77.83L230.34 79.02L230.34 84.99L230.03 86.18L229.11 87.29L227.65 88.25L191.87 106.14L189.96 106.87L187.74 107.33L185.36 107.48L182.98 107.33L180.76 106.87L178.86 106.14L126.81 80.11L125.35 79.16L124.43 78.05L124.12 76.86Z"/><path class="lo nf" d="M226.43 76.38L227.62 77.15L228.36 78.05L228.62 79.02L228.36 79.99L227.62 80.89L226.43 81.66L190.65 99.55L189.10 100.14L187.30 100.52L185.36 100.65L183.43 100.52L181.63 100.14L180.08 99.55L128.03 73.53L126.85 72.76L126.10 71.85L125.84 70.89L126.10 69.92L126.85 69.02L128.03 68.24L163.81 50.35L165.36 49.76L167.16 49.39L169.10 49.26L171.03 49.39L172.84 49.76L174.39 50.35Z"/><circle class="dot" cx="165.8" cy="66.0" r="1.4"/><circle class="dot m" cx="174.0" cy="70.1" r="1.4"/><circle class="dot m" cx="182.1" cy="74.1" r="1.4"/><circle class="dot m" cx="190.2" cy="78.2" r="1.4"/></g><g><path class="sil" d="M234.57 172.60L234.81 171.71L235.50 170.88L236.59 170.16L247.98 164.47L249.41 163.92L251.07 163.58L252.86 163.46L254.64 163.58L256.31 163.92L257.74 164.47L269.12 170.16L270.22 170.88L270.91 171.71L271.14 172.60L271.14 198.50L270.91 199.39L270.22 200.22L269.12 200.94L257.74 206.63L256.31 207.18L254.64 207.52L252.86 207.64L251.07 207.52L249.41 207.18L247.98 206.63L236.59 200.94L235.50 200.22L234.81 199.39L234.57 198.50Z"/><path class="lo nf" d="M267.90 170.77L268.72 171.31L269.24 171.93L269.42 172.60L269.24 173.27L268.72 173.90L267.90 174.43L256.52 180.12L255.44 180.54L254.20 180.79L252.86 180.88L251.52 180.79L250.27 180.54L249.20 180.12L237.81 174.43L236.99 173.90L236.47 173.27L236.30 172.60L236.47 171.93L236.99 171.31L237.81 170.77L249.20 165.08L250.27 164.67L251.52 164.41L252.86 164.32L254.20 164.41L255.44 164.67L256.52 165.08Z"/></g><g><path class="sil" d="M157.32 174.64L157.56 173.74L158.24 172.91L159.34 172.20L170.73 166.50L172.15 165.96L173.82 165.61L175.60 165.49L177.39 165.61L179.05 165.96L180.48 166.50L191.87 172.20L192.96 172.91L193.65 173.74L193.89 174.64L193.89 200.53L193.65 201.42L192.96 202.25L191.87 202.97L180.48 208.66L179.05 209.21L177.39 209.55L175.60 209.67L173.82 209.55L172.15 209.21L170.73 208.66L159.34 202.97L158.24 202.25L157.56 201.42L157.32 200.53Z"/><path class="lo nf" d="M190.65 172.81L191.47 173.34L191.99 173.97L192.16 174.64L191.99 175.31L191.47 175.93L190.65 176.47L179.26 182.16L178.19 182.57L176.94 182.83L175.60 182.92L174.27 182.83L173.02 182.57L171.95 182.16L160.56 176.47L159.74 175.93L159.22 175.31L159.05 174.64L159.22 173.97L159.74 173.34L160.56 172.81L171.95 167.11L173.02 166.70L174.27 166.44L175.60 166.36L176.94 166.44L178.19 166.70L179.26 167.11Z"/></g><g><path class="sil" d="M206.11 207.16L206.35 206.27L207.04 205.44L208.13 204.72L219.52 199.03L220.95 198.48L222.61 198.14L224.40 198.02L226.18 198.14L227.85 198.48L229.27 199.03L240.66 204.72L241.76 205.44L242.44 206.27L242.68 207.16L242.68 233.06L242.44 233.95L241.76 234.78L240.66 235.50L229.27 241.19L227.85 241.74L226.18 242.08L224.40 242.20L222.61 242.08L220.95 241.74L219.52 241.19L208.13 235.50L207.04 234.78L206.35 233.95L206.11 233.06Z"/><path class="lo nf" d="M239.44 205.33L240.26 205.87L240.78 206.49L240.95 207.16L240.78 207.83L240.26 208.46L239.44 208.99L228.05 214.68L226.98 215.10L225.73 215.35L224.40 215.44L223.06 215.35L221.81 215.10L220.74 214.68L209.35 208.99L208.53 208.46L208.01 207.83L207.84 207.16L208.01 206.49L208.53 205.87L209.35 205.33L220.74 199.64L221.81 199.23L223.06 198.97L224.40 198.88L225.73 198.97L226.98 199.23L228.05 199.64Z"/></g></svg><figcaption>控制面与节点<span>1 → N</span></figcaption></figure>
</section>

<section class="wrap band">
<div class="cards cards-4">
<div><h3>免证书</h3><p>默认入站是 VLESS + TCP + REALITY，借用公开网站的 TLS 握手，不需要自己的域名和证书。</p></div>
<div><h3>加人不断线</h3><p>新增、停用、到期的用户由控制面每 30 秒同步到节点，不重启节点上的代理核心。</p></div>
<div><h3>一条订阅地址</h3><p>同一条地址按客户端的 User-Agent 返回 Clash Meta、sing-box 或分享链接；浏览器打开是自助页。</p></div>
<div><h3>先查再改</h3><p><code>mpx status</code> 和 <code>mpx doctor</code> 只读不改，告诉你控制面、节点和每种订阅格式是否正常。</p></div>
</div>
</section>

<section class="wrap showcase">
<div class="showcase-row showcase-admin">
<div class="showcase-text">
<p class="eyebrow">管理员看到的</p>
<h2>用户、用量、到期，一张表</h2>
<p>本机控制面上的真实输出。用户多了用 <code>--status</code>、<code>--search</code> 筛选，默认每页 50 个。</p>
</div>
<div class="code"><div class="code-bar">Shell</div><pre class="cli-table">$ mpx user list
<span class="c" style="width:4ch">ID</span><span class="c" style="width:8ch">用户名</span><span class="c" style="width:6ch">可用</span><span class="c" style="width:8ch">状态</span><span class="c" style="width:6ch">已用</span><span class="c" style="width:7ch">上限</span><span class="c" style="width:12ch">到期</span><span class="c" style="width:10ch">重置策略</span><span class="c" style="width:6ch">入站</span><span class="c" style="width:4ch">备注</span>
<span class="c" style="width:4ch">1</span><span class="c" style="width:8ch">alice</span><span class="c" style="width:6ch">启用</span><span class="c" style="width:8ch">active</span><span class="c" style="width:6ch">0B</span><span class="c" style="width:7ch">100GB</span><span class="c" style="width:12ch">2026-12-31</span><span class="c" style="width:10ch">monthly</span><span class="c" style="width:6ch">全部</span><span class="c" style="width:4ch">演示</span>
共 1 个用户</pre></div>
</div>
<div class="showcase-row showcase-user">
<div class="showcase-text">
<p class="eyebrow">用户看到的</p>
<h2>浏览器打开订阅地址，就是自助页</h2>
<p>客户端拉同一条地址，拿到的是配置；浏览器打开，看到的是这一页：</p>
<ul class="showcase-list">
<li>状态徽章、已用流量和上限、到期日与剩余天数</li>
<li>各格式的订阅地址，每条带复制按钮，复制后有提示</li>
<li>第一条地址的二维码，由控制面直接生成，不依赖外部脚本</li>
<li>页面底部可以自己换订阅地址，换之前会先确认；换完旧地址立刻失效</li>
</ul>
</div>
<figure class="tile shot"><img class="only-light" src="/subscription-dashboard.png" width="1360" height="1882" alt="浏览器中的订阅页"><img class="only-dark" src="/subscription-dashboard-dark.png" width="1360" height="1882" alt="浏览器中的订阅页（深色）"><figcaption>订阅页<span>实测截图</span></figcaption></figure>
</div>
</section>

<section class="wrap start">
<h2>从这里开始</h2>
<div class="tbl"><table>
<tr><th>你要做什么</th><th>读这一页</th><th>类别</th></tr>
<tr><td>有一台 VPS，想尽快用起来</td><td><a href="./tutorial/quickstart">快速开始</a></td><td>教程</td></tr>
<tr><td>先不碰服务器，在本机看一遍</td><td><a href="./tutorial/local-trial">在本机试用</a></td><td>教程</td></tr>
<tr><td>加用户、发订阅</td><td><a href="./guides/add-users">添加用户并分发订阅</a></td><td>指南</td></tr>
<tr><td>某个客户端拿到的格式不对</td><td><a href="./guides/client-format">让某个客户端拿到指定格式</a></td><td>指南</td></tr>
<tr><td>有东西不工作</td><td><a href="./guides/troubleshoot">用 mpx doctor 排查</a></td><td>指南</td></tr>
<tr><td>查命令、字段、默认值</td><td><a href="./reference/cli">命令行</a> · <a href="./reference/settings">订阅设置</a> · <a href="./reference/subscription">订阅接口</a></td><td>参考</td></tr>
<tr><td>想知道它为什么这样分工</td><td><a href="./concepts/architecture">控制面与节点</a></td><td>原理</td></tr>
</table></div>
</section>
