# 让某个客户端拿到指定格式

某个客户端导入订阅后拿到的格式不对，比如拿到一串分享链接而不是 Clash 配置。

## 判断它现在拿到什么

控制面按客户端请求里的 User-Agent，从上到下匹配规则，第一条命中的决定格式。没有规则命中时返回分享链接（`links`），这不是错误。

```bash
mpx settings rules list
```

第一行写明规则来自「内置默认」还是「自定义」。

## 加一条规则

假设客户端的 User-Agent 以 `MyClient/` 开头，你要它拿 sing-box 格式：

```console
$ mpx settings rules add '(?i)^myclient/' -r sing-box --index 1
已加规则 1: (?i)^myclient/ → sing-box
```

`--index 1` 把它放到第一位。宽的规则会挡住后面的规则，例如 `(?i)clash` 也会命中 mihomo，所以具体的规则要放在前面。

`-r` 可选值：`clash-meta`、`clash`、`sing-box`、`xray`、`links`、`base64-links`、`block`。`block` 让该客户端拿到 404。

## 验证

```console
$ curl -s -A 'MyClient/2.0' -o /dev/null -w '%{content_type}\n' http://127.0.0.1:8080/sub/<token>
application/json; charset=utf-8
```

sing-box 格式返回 JSON。

## 改回默认

```console
$ mpx settings rules reset
已清掉自定义规则,回到内置默认
```

第一次改规则时，整张内置规则表会一起保存为自定义；之后内置规则更新不会再影响你。

## 不改规则的办法

让用户直接导入带格式后缀的地址，例如 `.../sub/<token>/clash-meta`。`mpx user links` 列出了全部后缀。
