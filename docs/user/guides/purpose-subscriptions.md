# 用途订阅：日常与流媒体分流

管理员可把授权节点分成用途候选组，再用域名规则分流。本批支持 Mihomo（clash-meta）和 sing-box；原始订阅地址和其他格式继续可用。以下操作沿用已配置的管理服务地址与 token。

## 1. 先标注节点或 host

查看 `mpx node list` 和 host 列表，确认要标注的 ID。为普通线路保存 `daily-node.json`：

```json
{"region":"US","line":"standard","purposes":["daily"]}
```

为流媒体线路保存 `media-node.json`：

```json
{"region":"US","line":"premium","purposes":["media"]}
```

```bash
mpx purpose metadata set nodes 1 -f daily-node.json
mpx purpose metadata set nodes 2 -f media-node.json
mpx purpose metadata get nodes 2
```

也可以将 `nodes` 换成 `hosts`，指定该 host 的元数据。host 的非空字段覆盖 node；写入 `{}` 清空覆盖。标签只筛选原本已经授权、未排水且状态可用的节点。

## 2. 创建日常与流媒体用途

保存 `daily.json`：

```json
{"slug":"daily","name":"日常","kind":"daily","tags":["daily"],"strategy":"url-test"}
```

保存 `media.json`：

```json
{
  "slug":"media","name":"流媒体","kind":"media","tags":["media"],
  "regions":["US"],"lines":["premium"],"strategy":"select",
  "domains":["netflix.com","nflxvideo.net","youtube.com","googlevideo.com"],
  "daily_purpose":"daily"
}
```

```bash
mpx purpose create daily -f daily.json
mpx purpose create media -f media.json
mpx purpose preview media alice clash-meta
mpx purpose preview media alice sing-box
```

media 域名先匹配流媒体候选组，其他代理流量走 daily 组，模板里的直连或拒绝决定保留。`daily_purpose` 留空时，其他流量沿用原模板。预览含用户凭证，应按订阅配置保存。`set` 完整替换配置，`del` 删除后旧用途地址返回明确错误。

## 3. 导入和验证

在浏览器打开 `/sub/<token>`，选择用途并复制对应客户端地址。固定地址为：

```text
https://proxy.example.com/sub/<token>/clash-meta?purpose=media
https://proxy.example.com/sub/<token>/sing-box?purpose=media
```

更新客户端配置后，在连接/路由详情中查看目标域名是否命中 media 组，并查看普通代理流量是否命中 daily 组。url-test 默认每 300 秒请求 HTTP 测试地址来比较延迟；它不能证明流媒体解锁、下载吞吐或游戏质量。download / game 类型须自己填写 domains 和候选条件，在本地测下载速度、游戏 RTT、抖动、丢包。

候选为空、日常用途被删除、账号停用/过期/超限或格式不支持时，会提示原因与默认订阅 URL；未知或已删除用途返回 404。默认订阅仍是 `/sub/<token>`，可从页面复制。

## 4. 自定义模板和能力阶段

用途的 `templates` 字段接受 `clash-meta` YAML 或 `sing-box` JSON 字符串，未设置时沿用全局模板。指定 `daily_purpose` 时，所选用途模板是两组共同使用的底稿。服务端注入已授权节点，再增加候选组和规则；模板格式契约见[全局配置字典](../reference/settings.md)。

首批可以只用手动标签，省略能力字段。接入检测后，可在完整用途 JSON 加入：

```json
{"required_capabilities":["egress","netflix"],"capability_region":"US"}
```

这是附加条件片段，需合并进完整配置再 `mpx purpose set media -f media.json`。要求全部能力满足；过期或未知结果不算可用。地区条件必须同时要求 egress 或 netflix，youtube 没有地区观测。未接入检测时，带能力要求的用途明确失败；保留空列表可继续手动阶段。下载与游戏仍依赖本地观测。
