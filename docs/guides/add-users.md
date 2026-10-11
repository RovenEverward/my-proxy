# 添加用户并分发订阅

给一个人开账号、限定流量和到期日，并把订阅地址发给他。

前提：`mpx login` 已连上控制面，至少有一个节点和入站。

## 建用户

```bash
mpx user add bob --limit 100GB --reset-strategy monthly --expire 2026-12-31
```

- `--limit`：流量上限，如 `100GB`、`1.5TB`；不写表示不限。
- `--reset-strategy`：`none`、`daily`、`weekly`、`monthly`。
- `--expire`：到期日，格式 `YYYY-MM-DD`，当地时区当天 23:59:59 截止。
- `--inbounds 1,2`：只允许连这些入站。**不写表示允许全部入站**，以后新建的入站也会自动对他开放。

新用户在 30 秒内同步到节点，不重启节点。

## 发订阅地址

```bash
mpx user links bob
```

把 `auto` 那一行发给他。客户端和浏览器打开同一条地址，各自拿到合适的内容。

## 验证

```bash
mpx user list
```

bob 的「可用」为「启用」、「状态」为 `active`，就可以用了。

## 常见调整

| 要做的事 | 命令 |
| :--- | :--- |
| 加流量 | `mpx user edit bob --limit 200GB` |
| 清零已用流量 | `mpx user reset-traffic bob` |
| 改到期日 | `mpx user renew bob ...`（参数见 `mpx user renew --help`） |
| 暂停使用 | `mpx user disable bob` |
| 订阅地址泄露了 | `mpx user revoke-sub bob`，再把新地址发给他 |
| 删除 | `mpx user rm bob` |

`revoke-sub` 只换订阅地址，已经连上的客户端不受影响。要立刻断开某人，用 `disable` 或 `rm`。
