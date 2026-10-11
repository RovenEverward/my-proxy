# 用 mpx doctor 排查

有东西不工作，先跑两条只读命令。它们不改任何东西。

```bash
mpx status   # 控制面版本、运行时间、后台任务、节点和用户数、退避中的节点
mpx doctor   # 逐项检查：数据库、后台任务、每台节点、六种订阅格式、流量采样
```

`mpx doctor` 每一项给出 `ok`、`warn` 或失败，并在说明里写下一步该做什么：

```console
$ mpx doctor
名称               结论    说明
数据库              ok    能读能写
后台任务             ok    上一轮 0s,距现在 13s
节点               warn  还没有任何节点 → mpx node add 加一台,再加一个入站才会有订阅
订阅 clash-meta    ok    渲染出 43243 字节
流量采样             ok    0 行(还没有流量经过,不是故障)

10 项检查,没有失败,1 项警告。
```

## 常见症状

| 症状 | 先看 | 处理 |
| :--- | :--- | :--- |
| 命令行报连不上 | `mpx login`（不带参数）显示当前地址和 token | 用 `mpx login --server ... --token ...` 重新保存 |
| 订阅里没有线路 | `mpx doctor` 的「节点」一项 | 加节点和入站后运行 `mpx node sync <id>` |
| 新建的入站不生效 | 是否运行过 `mpx node sync` | 结构改动只有 `sync` 后才生效；`sync` 会让该节点所有连接断一次 |
| 客户端拿到的格式不对 | `mpx settings rules list` | 见[让某个客户端拿到指定格式](/guides/client-format) |
| 浏览器打开订阅地址显示一串文字 | 请求是否带 `Accept: text/html` | 用普通浏览器打开；命令行工具默认不带这个头 |
