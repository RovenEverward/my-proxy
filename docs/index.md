---
layout: home
title: MPX
---

<section class="wrap hero">
<div class="hero-text">
<p class="eyebrow">MPX · 给自己和朋友用的自建代理</p>
<h1>在自己电脑上，<br>管好你那几台 <em>VPS</em></h1>
<p class="lede">装在 VPS 上的面板，得先登上去装，再开浏览器连过去配。MPX 是装在你自己电脑上的程序：填上 VPS 的 IP 和密码，它替你装好。之后开账号、加服务器、升级，都在这个窗口里点。</p>
<div class="cta">
<a class="btn btn-lg" href="./tutorial/quickstart#第-1-步-安装-mpx">下载 MPX</a>
<a class="btn-ghost" href="./tutorial/quickstart">安装步骤 →</a>
</div>
<p class="dl-note">macOS、Windows、Linux 都有，下载以 <code>mpx-desktop_</code> 开头的安装包。打开后填 VPS 的 IP 和 root 密码，装完直接给你订阅链接和二维码。也有命令行版。</p>
</div>
<figure class="tile hero-fig shot"><img class="only-light" src="/ui/setup-host.png" width="1280" height="820" alt="MPX 首次安装：填 VPS 的地址和 root 密码"><img class="only-dark" src="/ui/setup-host-dark.png" width="1280" height="820" alt="MPX 首次安装：填 VPS 的地址和 root 密码（深色）"><figcaption>首次安装：填 VPS<span>MPX 桌面窗口</span></figcaption></figure>
</section>

<section class="wrap band">
<div class="cards cards-4">
<div><h3>不用登 VPS</h3><p>装机、加服务器、加人、升级，都在你电脑上的窗口里点。</p></div>
<div><h3>几台机器，一个窗口</h3><p>加一台只填 IP 和密码，自动按所在地起名，例如「🇺🇸 洛杉矶 A」。哪台在线，节点页一眼就看到。</p></div>
<div><h3>朋友自己看用量</h3><p>每人有自己的流量上限、到期日，可以按月重置。他打开自己的链接就能看到还剩多少；链接泄露了，他自己能换一条。</p></div>
<div><h3>一条链接，常见客户端都能导入</h3><p>Clash Verge、sing-box、v2rayN、Shadowrocket 都认，复制链接或扫二维码即可。</p></div>
</div>
</section>

<section class="wrap showcase">
<div class="showcase-row showcase-admin">
<div class="showcase-text">
<p class="eyebrow">管理员看到的</p>
<h2>用户、用量、到期，一张表</h2>
<p>MPX 桌面窗口的用户页。按状态和套餐筛选，快到期、快用完的人单独列在总览里。</p>
</div>
<figure class="tile shot"><img class="only-light" src="/ui/users.png" width="1280" height="820" alt="MPX 桌面窗口的用户页"><img class="only-dark" src="/ui/users-dark.png" width="1280" height="820" alt="MPX 桌面窗口的用户页（深色）"><figcaption>用户页<span>MPX 桌面窗口</span></figcaption></figure>
</div>
<div class="showcase-row showcase-user">
<div class="showcase-text">
<p class="eyebrow">用户看到的</p>
<h2>浏览器打开订阅地址，就是自助页</h2>
<p>客户端拉同一条地址，拿到的是配置；浏览器打开，看到的是这一页：</p>
<ul class="showcase-list">
<li>状态徽章、已用流量和上限、到期日与剩余天数</li>
<li>各格式的订阅地址，每条带复制按钮，复制后有提示</li>
<li>带二维码，手机扫一下就行</li>
<li>页面底部可以自己换订阅地址，换之前会先确认；换完旧地址立刻失效</li>
</ul>
</div>
<figure class="tile shot"><img class="only-light" src="/subscription-dashboard.png" width="1360" height="1882" alt="浏览器中的订阅页"><img class="only-dark" src="/subscription-dashboard-dark.png" width="1360" height="1882" alt="浏览器中的订阅页（深色）"><figcaption>订阅页<span>实测截图</span></figcaption></figure>
</div>
<div class="showcase-row showcase-admin">
<div class="showcase-text">
<p class="eyebrow">加服务器</p>
<h2>加一台服务器：填 IP 和密码</h2>
<p>节点页点「添加节点」，填地址和 root 密码。装核心、建入站、起名一路做完，完成页告诉你这台进了哪些订阅。</p>
</div>
<figure class="tile shot"><img class="only-light" src="/ui/node-wizard.png" width="1280" height="820" alt="添加节点向导：填服务器地址、节点名和 SSH 密码"><img class="only-dark" src="/ui/node-wizard-dark.png" width="1280" height="820" alt="添加节点向导：填服务器地址、节点名和 SSH 密码（深色）"><figcaption>添加节点<span>MPX 桌面窗口</span></figcaption></figure>
</div>
<div class="showcase-row showcase-admin">
<div class="showcase-text">
<p class="eyebrow">分流</p>
<h2>哪些网站走代理，在界面里排</h2>
<p>一张从上往下的规则表，每条选直连、拦截或者走哪组节点。可以给某个人单独加规则，也能输入一个网址，测它会走哪条。</p>
</div>
<figure class="tile shot"><img class="only-light" src="/ui/rules.png" width="1280" height="820" alt="分流规则页：规则从上往下匹配，右侧测试一个网址"><img class="only-dark" src="/ui/rules-dark.png" width="1280" height="820" alt="分流规则页：规则从上往下匹配，右侧测试一个网址（深色）"><figcaption>分流规则<span>MPX 桌面窗口</span></figcaption></figure>
</div>
<div class="showcase-row showcase-admin">
<div class="showcase-text">
<p class="eyebrow">升级</p>
<h2>升级：点一次</h2>
<p>有新版本时右上角出现「更新」。点一次，服务器、各节点和你电脑上的 MPX 按顺序升好，窗口自己刷新。升级某台节点时，那台上的连接会断开一次，客户端会自动重连。</p>
</div>
<figure class="tile shot"><img class="only-light" src="/ui/update.png" width="1280" height="820" alt="更新弹窗：列出桌面程序、控制面和各节点的当前版本与目标版本"><img class="only-dark" src="/ui/update-dark.png" width="1280" height="820" alt="更新弹窗：列出桌面程序、控制面和各节点的当前版本与目标版本（深色）"><figcaption>一键更新<span>MPX 桌面窗口</span></figcaption></figure>
</div>
</section>

<section class="wrap start">
<h2>从这里开始</h2>
<div class="tbl"><table>
<tr><th>你要做什么</th><th>读这一页</th><th>类别</th></tr>
<tr><td>有一台 VPS，想尽快用起来</td><td><a href="./tutorial/quickstart">快速开始</a></td><td>教程</td></tr>
<tr><td>加用户、发订阅</td><td><a href="./guides/add-users">添加用户并分发订阅</a></td><td>指南</td></tr>
<tr><td>再加一台服务器</td><td><a href="./guides/add-nodes">添加节点</a> · <a href="./guides/plans">用套餐分配节点</a></td><td>指南</td></tr>
<tr><td>决定哪些网站走代理</td><td><a href="./guides/routing">设置分流规则</a></td><td>指南</td></tr>
<tr><td>在另一台电脑上管理</td><td><a href="./guides/devices">在多台设备上管理</a></td><td>指南</td></tr>
<tr><td>升级</td><td><a href="./guides/update">点一次更新</a></td><td>指南</td></tr>
<tr><td>有东西不工作</td><td><a href="./guides/troubleshoot">用自检排查</a></td><td>指南</td></tr>
<tr><td>查页面、设置项、订阅格式</td><td><a href="./reference/interface">界面</a> · <a href="./reference/settings">订阅设置</a> · <a href="./reference/subscription">订阅接口</a></td><td>参考</td></tr>
<tr><td>想知道原理（证书、订阅格式、流量怎么走）</td><td><a href="./concepts/architecture">控制面与节点</a></td><td>原理</td></tr>
</table></div>
</section>
