# 用户文档导航 (User Documentation)

本文档索引面向使用 `mpx` 系统的管理员与终端代理用户的全部帮助手册。遵循 [Diátaxis](https://diataxis.fr/) 规范编排。

---

## 1. 新手教程 (Tutorial)
面向新手，按线性步骤完成一个成功的端到端目标。
- [从零搭建多节点代理管理服务](tutorial/getting-started.md)：从一台裸机控制面和一台节点开始，跑通 VLESS REALITY 协议并分发首个用户订阅。**只有一台 VPS 的话，开头的「懒人路径」一条 `mpx init` 命令就能跑完全部步骤。**

---

## 2. 操作指南 (How-to Guides)
面向已有基础的人，解决具体运维场景或终端使用任务。

### 终端代理用户
- [客户端订阅导入与使用指南](guides/client-setup.md)：Clash Verge、sing-box、v2rayN、Shadowrocket 等客户端导入教程，以及网页订阅仪表盘与自助换 Token 说明。

### 控制面管理员
- [节点运维与生命周期管理](guides/node-operations.md)：节点安装、配置下发（Sync）、平滑维护排空（Drain）与实时在线活跃 IP 查询。
- [Hysteria2 与 TUIC 节点](guides/quic-protocols.md)：第二核心安装、证书、订阅、独立计量与用户停用的重启边界。
- [用户与配额管理](guides/user-management.md)：用户增删改查、配额限制、周期自动重置、到期顺延与 CSV 批量导入导出。
- [告警与到期通知配置](guides/notifications.md)：Webhook 回调地址配置、签名验证、超额/到期/掉线事件推送说明。
- [备份与灾备恢复实操](guides/backup-and-restore.md)：在线热备份、数据包校验、暂存换入与新服务器跨机迁移拉起。
- [故障排查手册 (Troubleshooting)](guides/troubleshooting.md)：`mpx doctor` 各诊断项处置方案，以及订阅无法拉取、节点退避等典型故障速查。

---

## 3. 参考速查 (Reference)
客观技术事实、字段定义与机器格式参考。
- [全局配置字典 (Settings)](reference/settings.md)：系统设置全部字段、默认值说明与 User-Agent 自动分流匹配规则。
- [协议与传输层支持矩阵](reference/protocols.md)：协议、传输方式与安全层组合约束，以及各客户端格式特性支持清单。
- [用户 CSV 导入导出规格 (CSV Format)](reference/csv-format.md)：批量用户 7 列数据格式、缺省语义与 Upsert 覆盖机制。
