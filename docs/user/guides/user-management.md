# 用户与配额管理

本文介绍如何创建、停用、批量导出导入用户，以及如何管理流量重置周期与到期时间。

---

## 1. 单个用户管理

### 创建用户

```bash
mpx user add alice \
  --limit 100GB \
  --expire 2026-12-31 \
  --reset-strategy monthly
```

关键参数说明：
- `--limit`：流量总额，支持单位 `MB`、`GB`、`TB`（按 1024 进制解析）。未指定时为不限流量。
- `--expire`：过期时间，格式支持 `YYYY-MM-DD` 或 `YYYY-MM-DDTHH:MM:SS`。
- `--reset-strategy`：周期性流量重置策略。支持 `never`（不重置，默认）、`monthly`（自然月第一天 00:00 自动重置已用流量）。

### 调整配额与续期

```bash
# 增加/调整配额
mpx user edit alice --limit 200GB

# 为用户延长有效期（如续期 30 天）
mpx user renew alice --days 30

# 手动将已用流量清零
mpx user reset-traffic alice
```

### 停用与启用

```bash
# 临时停用（客户端订阅仍能拉取，但只返回 0.0.0.0 占位节点，节点上凭证立失效）
mpx user disable alice

# 恢复使用
mpx user enable alice
```

### 撤销订阅 Token

如果管理员怀疑该用户的订阅链接被外泄并滥用：

```bash
mpx user revoke-sub alice
```
执行后，原订阅链接当场作废，系统生成新的随机 SubToken。底层代理协议的 UUID/密码保持不变。

---

## 2. 批量用户操作（CSV 导入与导出）

mpx 原生支持通过 CSV 格式实现批量数据迁移、多用户开卡与系统备份。

### 导出用户数据

```bash
# 导出全量用户到标准输出
mpx user export

# 导出并保存到本地文件
mpx user export > users.csv
```

导出的 CSV 具有严格的 7 列规范（详见 [CSV 规范参考手册](../reference/csv-format.md)）：
```csv
username,status,data_limit_bytes,used_traffic_bytes,expire_at,reset_strategy,sub_token
alice,active,107374182400,10485760,2026-12-31T00:00:00Z,monthly,a1b2c3d4e5f6g7h8...
```

### 批量导入用户

```bash
# 从 CSV 文件中批量导入用户
mpx user import < users.csv
```

**导入行为约定**：
- **用户名冲突时**：以 CSV 中的内容覆盖已有用户的属性（包括配额、到期日与重置策略）。
- **字段为空时**：系统自动使用默认值填补（例如留空 `sub_token` 会自动生成高强度随机 Token，留空 `status` 默认为 `active`）。
- **导入后同步**：导入完成后，控制面后台将在下一个扫描周期内全自动向活跃节点下发新用户的凭证。
