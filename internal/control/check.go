package control

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// checkBudget 是查一台节点状态的上限。
//
// 它比采样预算(20 秒)松、比下发预算(2 分钟)紧:这条命令只读几个文件、跑一次
// openssl,正常不到一秒;给 30 秒是给慢链路和 openssl 在磁盘上卡一下留的余量。
// 巡检要用它,而巡检不能把一轮 30 秒的周期拖长 —— 30 秒是"这台节点这一轮最多
// 花多久",不是"预期耗时"。
const checkBudget = 30 * time.Second

// CheckNode 现场问一台节点它现在怎么样:服务在不在跑、核心什么版本、配置的哈希
// 是多少、每张证书还有几天到期。
//
// 关键是**只发一条 ssh**。四次往返和一次往返的差别不只是快:分开问的话,四条
// 命令分别落在四个瞬间,拼出来的"状态"可能是四个不同时刻的快照,而漂移判定用的
// 是哈希和配置内容的一致性 —— 拿一个自相矛盾的状态去做判断,比没有状态更糟。
// 一条脚本里所有输出在同一时刻产生,代价只是脚本本身要小心写。
//
// 返回的 error 只留给「连状态都算不出来」的情况(读库失败、渲染脚本失败)。
// 节点连不上不是 error:那是一份 Reachable=false 的**有效状态**,它照样要落库、
// 照样要进 doctor。把它做成 error,调用方就只能丢掉它 —— 而"节点失联"恰恰是
// 最该说出来的一件事。
func (c *Control) CheckNode(ctx context.Context, n model.Node, now time.Time) (model.NodeStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, checkBudget)
	defer cancel()

	inbounds, err := c.store.ListInbounds(n.ID)
	if err != nil {
		return model.NodeStatus{}, err
	}

	st := model.NodeStatus{NodeID: n.ID, CheckedAt: now.UTC(), Certificates: []model.CertificateInfo{}}

	out, err := c.runner.Run(ctx, n, checkScript(n, inbounds))
	if err != nil {
		// 连不上或超时:状态本身就写着"问不到",不是计算失败。
		st.Reachable = false
		st.Error = err.Error()
		return st, nil
	}

	st.Reachable = true
	parseCheckOutput(out, &st)

	// 证书是按入站 id 认回来的(脚本只带 id,免得把一个可能带空格和引号的名字
	// 塞进 shell),名字要在这里补上 —— 不然 node status 打出来的"入站"一栏
	// 全是数字,使用者还得回库里去查这个 id 是哪个入站。
	names := make(map[int64]string, len(inbounds))
	for _, in := range inbounds {
		names[in.ID] = in.TagOrDefault()
	}
	for i := range st.Certificates {
		if st.Certificates[i].InboundName == "" {
			st.Certificates[i].InboundName = names[st.Certificates[i].InboundID]
		}
	}
	return st, nil
}

// checkScript 拼出那一条 ssh 脚本。
//
// 每个值都带一个固定的前缀(k=...),控制面按前缀取值而不是按行号 —— 节点上
// systemctl 的告警、openssl 的报错都可能混进 stdout,按行号取值会在那些时候
// 整体错位,而按前缀取最多丢掉那一项。
//
// 每一项都盯着自己的失败:`|| echo` 把"命令没成"也变成本行输出,于是"服务没跑"
// "没有 openssl""证书文件不在"都会明确地写出来。任何一项静默消失,控制面都会
// 把"没问到"当成"没问题"—— 一个看不见缺失的检查比不检查更危险。
func checkScript(n model.Node, inbounds []model.Inbound) string {
	var b strings.Builder
	// 和 node 包同一套核心路径约定:装在哪、测试时可用 MPX_XRAY_BIN 改到别处。
	// 这里手写一遍前缀而不导出 node 包的内部常量,是为了让脚本在控制面这一侧
	// 完整可读 —— 它的每一行都要和节点上的现实对得上。
	b.WriteString(`XRAY_BIN="${MPX_XRAY_BIN:-/usr/local/lib/mpx/xray}"; `)
	b.WriteString("echo k=service:$(systemctl is-active " + shellQuote(n.ServiceNameOrDefault()) + " 2>/dev/null || echo unknown); ")
	// version 的输出是 "Xray 26.3.27 (Xray, ...) d2758a0 (...)",取第 2 段并去掉 v
	// 前缀,和 node.Version 一个取法:整行读不下来,给使用者看的只需要版本号。
	b.WriteString("echo k=version:$(\"$XRAY_BIN\" version 2>/dev/null | head -1 | awk '{print $2}' | sed 's/^v//'); ")
	// 哈希取不到(文件不在、路径写错)时输出 hasherr,而不是一个空值:空值会让
	// 漂移判定拿 "" 和 deployed_hash 比,凭空报出一个"漂移"。
	//
	// 先把 sha256sum 的输出接住再判定,不能写成 `$(sha256sum X | cut ... || echo hasherr)`:
	// `||` 跟的是整条管道,而管道最后一段是 cut —— sha256sum 失败时 cut 照样成功
	// 退出,哨兵永远不会出现,值只是一个空串。
	b.WriteString("hash=$(sha256sum " + shellQuote(n.ConfigPathOrDefault()) + " 2>/dev/null | cut -d' ' -f1); ")
	b.WriteString("echo k=hash:${hash:-hasherr}; ")

	// 一个入站一行。用入站 id 而不是 tag 认领:tag 可能含空格和引号,id 是控制面
	// 自己发的整数,对得上就不会张冠李戴。
	for _, in := range inbounds {
		if strings.TrimSpace(in.CertFile) == "" {
			continue
		}
		id := strconv.FormatInt(in.ID, 10)
		p := in.CertFile
		// 先确认文件在:openssl 对不存在的文件也会报错,但那句话里没有"是哪个
		// 路径",而证书路径是使用者唯一能照着去查的东西。
		b.WriteString("echo k=certpath:" + id + "=$(test -r " + shellQuote(p) + " && echo " +
			shellQuote(p) + " || echo '读不到:文件不存在或没有权限'); ")
		// notAfter=Nov  4 12:00:00 2026 GMT。
		//
		// 先把 openssl 的输出和退出码都接住,再分两行报出去。写成
		// `echo k=certend:$(openssl ...); echo k=certok:$?` 是不行的:后者拿到的是
		// 上一条 echo 的退出码,永远是 0 —— 于是"节点上没有 openssl"和"文件不是
		// 证书"就都退化成同一句"没读成",而这两种情况该做的事不一样。
		b.WriteString("certout=$(openssl x509 -noout -enddate -in " + shellQuote(p) + " 2>/dev/null); certrc=$?; ")
		b.WriteString("echo k=certend:" + id + "=${certout#notAfter=}; ")
		b.WriteString("echo k=certok:" + id + "=$certrc; ")
	}
	return b.String()
}

// parseCheckOutput 把脚本的 key=value 行读成一份状态。
//
// 认不出的行直接忽略:节点上的 systemd 或 shell 会往 stdout 里塞别的东西
// (locale 警告、motd、service 的提示),那些不是错误,只是噪音。
func parseCheckOutput(out string, st *model.NodeStatus) {
	certs := map[int64]*model.CertificateInfo{}
	var order []int64

	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "k=") {
			continue
		}
		key, value, ok := strings.Cut(strings.TrimPrefix(line, "k="), ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)

		// 证书那几行的 key 里还带着入站 id(`certpath:7`),值在第二个冒号后面。
		// 所以这里先看前缀:命中就按 id 拆,剩下的才是值。
		if kind, id, rest, ok := splitCertKey(key, value); ok {
			applyCertLine(kind, id, rest, certs, &order)
			continue
		}

		switch {
		case key == "service":
			st.Active = value == "active"
		case key == "version":
			st.Version = value
		case key == "hash":
			// hasherr 是脚本在 sha256sum 没成时写的哨兵值,不是真的哈希。空着
			// 落库,漂移判定就不会拿它去比。
			if value != "hasherr" {
				st.ObservedHash = value
			}
		}
	}

	// 按入站 id 升序落库。脚本本来就是照 ListInbounds 的顺序输出,但解析的产物
	// 该只由输入内容决定:哪天输出的拼法变了(并发拼、加了别的行),证书顺序也
	// 不该跟着变。
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
	for _, id := range order {
		st.Certificates = append(st.Certificates, *certs[id])
	}
	for i := range st.Certificates {
		c := &st.Certificates[i]
		if c.NotAfter == nil {
			if c.Error == "" {
				c.Error = "证书的到期时间没读到"
			}
			continue
		}
		// 日期都读出来了,就说明这一条读成了:清掉之前那行退出码留下的报错,
		// 让"有到期时间"和"有错误"不会同时成立。
		c.Error = ""
		// 向下取整到天:剩 13.9 天时使用者该看到的"还有 13 天",报 14 天会让
		// 他在第 14 天以为还早。floor 也让"今天到期"落在 0 而不是 1。
		days := int(c.NotAfter.Sub(st.CheckedAt).Hours() / 24)
		c.DaysLeft = days
	}
}

// splitCertKey 拆开证书那几行。
//
// 脚本写的是 `k=certpath:7=/etc/mpx/xray/certs/hk.pem`:入站 id 夹在 key 和值之间,
// 所以通用解析(在第一个冒号处切)拿到的是 key="certpath"、value="7=/etc/...",
// id 会被吞进值里 —— 每张证书都并到同一个槽位。这里把 id 单独拆出来,顺带把
// 是哪一行(certpath / certend / certok)也带出去:值长什么样不该用来猜它是哪一行。
//
// 认不出返回 ok=false,让调用方按普通 key 继续处理(而不是悄悄丢掉一行)。
func splitCertKey(key, value string) (kind string, id int64, rest string, ok bool) {
	for _, k := range []string{"certpath", "certend", "certok"} {
		raw, found := strings.CutPrefix(key, k)
		if !found {
			continue
		}
		switch {
		case raw == "":
			// id 和值挤在同一个字段里:`certpath:7=/path`。
			raw, rest, _ = strings.Cut(value, "=")
		default:
			if i := strings.Index(raw, "="); i >= 0 {
				raw, rest = raw[:i], raw[i+1:]
			} else {
				rest = value
			}
		}
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return "", 0, "", false
		}
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			// 认不出的 id 归到 0:这一条仍然会被报出来(带路径),只是没有入站可指。
			n = 0
		}
		return k, n, strings.TrimSpace(rest), true
	}
	return "", 0, "", false
}

// applyCertLine 把证书的某一行落到它那条记录上。
//
// 三行各管一件事,按行名分派而不按值猜:certend 的值一定是个日期、certok 的值
// 一定是个退出码,靠值的形状去认行(比如"以 / 开头就是路径")只是碰巧现在成立。
func applyCertLine(kind string, id int64, value string, certs map[int64]*model.CertificateInfo, order *[]int64) {
	c, ok := certs[id]
	if !ok {
		c = &model.CertificateInfo{InboundID: id}
		certs[id] = c
		*order = append(*order, id)
	}
	if value == "" {
		// 空值不覆盖:三行谁都可能先到,谁都可能没内容。
		return
	}

	switch kind {
	case "certpath":
		c.Path = value
	case "certend":
		if notAfter, err := parseOpenSSLDate(value); err == nil {
			c.NotAfter = &notAfter
		}
	case "certok":
		switch value {
		case "0":
			// 读成了,没什么要记的。
		case "127":
			// 127 是 shell 的"命令不存在":节点上没装 openssl。这和"文件不是证书"
			// 要做的处理不一样(装一个 openssl / 换一份文件),所以分开说。
			c.Error = "节点上没有 openssl,读不到证书的到期时间"
		default:
			c.Error = "读不到证书的到期时间(文件不是证书,或者已经损坏)"
		}
	}
}

// parseOpenSSLDate 解析 openssl -enddate 输出里的 notAfter(如 "Nov  4 12:00:00 2026 GMT")。
//
// 刻意只认这一个格式:它是 openssl 的固定输出。换了格式说明节点上的 openssl 不是
// 我们预期的那一个,那时宁可报"读不到"让人去看,也不要猜一个日期出来 ——
// 猜错的方向如果是"还早",真到期时就没人管了。
func parseOpenSSLDate(v string) (time.Time, error) {
	if v == "" {
		return time.Time{}, fmt.Errorf("到期时间为空")
	}
	// openssl 用两个空格补个位数日期("Nov  4"),time.Parse 的 "Jan _2" 正是
	// 容忍空格填充的那个格式。
	return time.Parse("Jan _2 15:04:05 2006 MST", v)
}

// shellQuote 把一个值安全地放进单引号里。
//
// node 包里有一个同名函数,但它没有导出,而且这里不该依赖它:脚本是控制面这一侧
// 拼的,凡是拼进 shell 的东西都必须经过引号处理。配置路径和证书路径都来自库里
// (也就是来自使用者输入),其中任何一个含空格或分号,不引起来就会变成一条
// 完全不同的命令 —— 而这条命令是在别人的机器上跑的。
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
