# 设备接入:一行命令起控制面,多台设备用设备密码接入

状态:Accepted(2026-10-15 用户批准,含控制面 API 改动)｜实现:M1–M3 进行中,M4 的 UI 部分等看截图再做。

## 结论

- **用户永远不用登录 VPS 操作。** `mpx init` 在用户设备上经 SSH 远程把一台 VPS 装成控制面(+ 节点),过程中设**设备密码**,结束时打印**一行接入命令**:
  `mpx connect 203.0.113.10:8080 --fp SHA256:3q2+7w…`
- 所有设备(包括跑 init 的那台)都用 `mpx connect` 接入:输设备密码 → 换一枚**这台设备专属的 token**。之后不需要 SSH。
- 控制面自己生成自签证书,管理接口**只走 HTTPS**,CLI 按证书指纹固定(同 SSH known_hosts)。不需要域名、反代、CA 证书。
- **单端口**:8080 上按首字节嗅探,TLS 握手 → HTTPS(管理),其余 → HTTP(订阅)。
- 管理 token(`/etc/mpx/env`)和 `mpx login --token` 保留作应急入口,不写进安装说明。

## 1. 起点(改动前的代码事实,main @ 6d163fb)

- 一个 `http.Server` + `ListenAndServe`,`/sub` 与 `/api` 同一端口同一 mux,无 TLS。`mpx init` 装在 `0.0.0.0:8080`,公网明文。
- `/api` 只认一个固定 Bearer token;它只在 VPS 的 `/etc/mpx/env` 里,第二台设备只能 SSH 去取。
- `requireAuth(rateLimit(api))`:鉴权在限流外层,撞 token 不被限流。
- `mpx login` 只是写本地配置档;`mpx ui` 用配置档的 token 在本机转发,token 不进浏览器。
- 备份只搬库(+ 节点 SSH 私钥),恢复只换库。

## 2. 设计

### 2.1 单端口嗅探(M1)
TLS 记录首字节恒为 `0x16`,HTTP/1.x 请求以大写方法名开头,偷看一个字节即可分流。net/http 拿到 `*tls.Conn` 会自己握手并填 `r.TLS`。只说 HTTP/1.1。可行性已用真实监听测过(`internal/server/sniff_test.go`:同端口明文/TLS 交错与长连接复用、错指纹拒绝、只连不发的连接不挡 Accept)。

### 2.2 自签证书与指纹(M1)
ECDSA P-256,10 年,存库(`tls_identity`)而非文件——随备份恢复,指纹不变。指纹 = SPKI 的 SHA-256,`SHA256:<base64>`。控制面启动时另写一份 `<库目录>/tls-fingerprint` 纯文本,供 init 经 SSH 读回;`mpx status` 也显示。CLI 配置档多一个 `fingerprint`;`https://` 地址没有指纹直接报错,不符拒绝连接。`mpx ui` 转发用同一个只认指纹的传输。

### 2.3 设备 token(M2)
表 `devices(id, name, token_hash, created_at, last_used_at, last_ip)`;token 32 字节随机,库里只存 SHA-256。鉴权:先比管理 token(主钥匙,不可吊销、不在列表里),再查设备表。`last_used_at` 每设备至多每分钟写一次。`GET /api/devices`、`DELETE /api/devices/{id}`;`mpx device list / revoke`。同时把 `/api` 改成先限流再鉴权。

### 2.4 设备密码与 `mpx connect`(M3)
- 密码哈希:argon2id(`golang.org/x/crypto/argon2`),PHC 字符串存库。长度 ≥ 12。
- `mpx init` 末尾问两次设备密码,经 SSH 在 VPS 本机设置(不经公网),然后在本机自动 connect,最后打印给其他设备的一行。
- `mpx connect <地址> [--fp 指纹]`:没给 `--fp` 时先显示对方指纹、要用户输入 yes 确认(TOFU);给了就只认它。然后输入设备密码、设备名(默认主机名)→ `POST /api/connect` → 存配置档 `{server, token, fingerprint}`。
- `mpx device password`:已接入的设备上改密码;改密码不吊销已发出的 token。
- 防爆破:`/api/connect` 每 IP 每分钟 5 次、全局每分钟 20 次;连续失败 20 次全局锁 15 分钟(已接入设备不受影响)。
- 旧安装迁移:`mpx connect --upgrade`(已有 http:// 配置档时)用旧 token 经旧通道读一次指纹,把配置档改成 https + 指纹;没设设备密码的,用 `mpx device password` 设一次。

### 2.5 明文管理的过渡
本版本:非回环的明文 `/api` 仍可用,带 `X-Mpx-Warning` 头,CLI 打印一次提醒。下一个版本:拒绝。

### 2.6 订阅
留在 HTTP(同一端口)。代理客户端普遍不认自签证书。已有 `SubURLPrefix` 不受影响。订阅 token 链路明文是已知限制。

### 2.7 证书轮换
证书 10 年,默认不轮换。需要轮换(私钥泄露)时:换一份新身份,所有设备重新 `mpx connect`。命令放 M4。

## 3. 里程碑
- **M1** 自签证书存库、单端口嗅探、CLI 指纹固定(`--fp`/配置档/`mpx ui`)、明文管理告警、`mpx status` 显示指纹、init/install 输出改为 https + 指纹。
- **M2** 设备 token、多 token 鉴权、限流顺序修正、`mpx device list/revoke`。
- **M3** 设备密码、`POST /api/connect` + 防爆破、`mpx connect`(含 TOFU)、`mpx device password`、init 设密码并自动接入、`connect --upgrade`、文档。
- **M4** UI 设备页(先给截图)、证书轮换命令、下一个版本关闭明文管理。

## 4. 已拍板
1. 单端口嗅探,不开 8443。
2. 旧安装明文管理保留一个版本并警告。
3. `connect` 不带 `--fp` 时显示指纹让用户确认。
4. 保留管理 token 与 `mpx login --token` 作应急入口,不写进安装说明。
5. 证书 10 年;轮换后重新 connect。

## 5. 未核实
- 各订阅客户端对自签 HTTPS 的实际支持未逐个测。
- 少数中间盒/云 WAF 可能对同端口混跑 TLS 与 HTTP 有特殊处理,未在真实云环境测。
