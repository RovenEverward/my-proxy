# 备份与灾备恢复实操

mpx 的状态存储为单一 SQLite 数据库（默认路径 `/var/lib/mpx/data.db`）。本文介绍在线热备份、数据库校验与从灾难中跨机恢复的完整实操流程。

---

## 1. 制作备份

使用 `mpx backup` 命令可以在系统运行中无锁热备份：

```bash
# 备份并保存为带时间戳的文件
mpx backup -o /tmp/mpx-backup-$(date +%Y%m%d%H%M%S).tar.gz
```

生成的备份归档包包含两个部分：
1. **`db.sqlite`**：通过 SQLite 官方在线备份 API（`VACUUM INTO`）生成的只读完整数据库快照。
2. **`meta.json`**：包含备份创建时间、mpx 内部 schema 版本号以及对象校验哈希。

> **自动化定时备份推荐**：
> 在控制面服务器上配置 crontab：
> ```bash
> 0 3 * * * /usr/local/bin/mpx backup -o /var/backups/mpx/daily-$(date +\%Y\%m\%d).tar.gz
> ```

---

## 2. 验证备份完整性

在将备份归档或用于恢复前，建议先验证其完整性：

```bash
mpx restore --verify /tmp/mpx-backup-20261009115000.tar.gz
```

输出：
```
备份包有效: Schema 版本 4, 包含 5 个节点, 12 个入站, 45 个用户. 数据库完整性检查: OK.
```

---

## 3. 在线恢复（暂存与安全换入）

当由于误操作导致数据受损，且控制面服务正在运行时，执行恢复：

```bash
mpx restore /tmp/mpx-backup-20261009115000.tar.gz
```

**内部执行机制（防损坏保证）**：
1. 系统将备份解压并写入临时文件 `/var/lib/mpx/data.db.staged`。
2. 对暂存文件运行完整性校验。
3. 安全原子替换（`rename`）当前工作数据库，并通知正在运行的后台进程热重载配置。

---

## 4. 跨机器全新灾难恢复

当原控制面服务器硬件故障，需要在新服务器上快速拉起控制面：

### 步骤 1：新机器安装基础环境

在新服务器上执行全新安装：
```bash
sudo ./deploy/install.sh /usr/local/bin/mpx
```

### 步骤 2：恢复数据库

停止服务，将备份包解压替换数据库：
```bash
sudo systemctl stop mpx
sudo mpx restore --force /path/to/mpx-backup.tar.gz
sudo systemctl start mpx
```

### 步骤 3：重设 SSH 凭证与公钥分发

由于换了新服务器，新机器生成的 SSH 密钥不同于老机器：
1. 查看新机器的 SSH 公钥：
   ```bash
   cat /var/lib/mpx/.ssh/id_ed25519.pub
   ```
2. 将此公钥登录追加到所有存量节点的 `authorized_keys` 中。
3. 运行连通性核验：
   ```bash
   mpx doctor
   ```
4. 执行一次全局节点同步，重新接管状态：
   ```bash
   mpx node sync
   ```
