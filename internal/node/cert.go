package node

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// EnsureTLSCertificates 保证节点上每个 TLS 入站引用的 (cert,key) 就位。
//
// 这是「装得上」的那半步:sing-box / xray 校验配置时会去读证书文件,缺文件
// 的 sync 在 sing-box check 阶段就挂掉。在控制面备好、经 SSH 原子写下去,
// 让「建 TLS 入站 → sync」不再需要操作者登到节点上跑 openssl。
//
// 两类材料走同一条下发路径,只是来源不同:
//   - 入站带了 cert_pem/key_pem(UC-27,控制面托管的真实证书):以库里的内容
//     为准,按哈希比对,不一致就覆盖 —— 续期换来的新证书靠这条路落到节点;
//   - 没带(旧语义):缺失时按 SNI 在控制面签自签(同一 SNI 只签一对,写进引用
//     它的每个路径);两文件都在的一对一字节不动 —— 节点上已有的证书不被覆盖,
//     半对(只剩一个文件)整对重签。
//
// 三件事按这个顺序做:
//  1. 一条 SSH 报出每对的状态与内容哈希;
//  2. 决定要写哪几对(缺失、半对、或托管内容不一致),备好材料;
//  3. PutFile 先写 .tmp 再 mv,写完 chmod 成约定权限。cert 0644(sing-box 以
//     自己的账户跑时要能读)、key 0600。
//
// 返回值报「这轮有没有动过需要重新加载的证书材料」。只有托管证书(cert_pem)
// 的写入才算:续期换上来的新证书必须重启核心才能读到;补齐自签不算 —— 运行中
// 的 sing-box 内存里已经有它启动时加载的那张,客户端本来也跳过校验,重不重启
// 对外都一样。证书变化不进 config.json,光靠配置哈希比对会漏掉这条信号。
func EnsureTLSCertificates(ctx context.Context, r Runner, n model.Node, inbounds []model.Inbound) (reload bool, err error) {
	type pair struct {
		cert, key     string
		sni           string
		certPEM       string
		keyPEM        string
	}
	pairs := make(map[string]pair) // cert 路径 → pair(cert 路径即 key 的唯一标识)
	for _, in := range inbounds {
		if in.Security != model.SecurityTLS || in.CertFile == "" || in.KeyFile == "" {
			continue
		}
		pairs[in.CertFile] = pair{cert: in.CertFile, key: in.KeyFile, sni: in.SNI,
			certPEM: in.CertPEM, keyPEM: in.KeyPEM}
	}
	if len(pairs) == 0 {
		return false, nil
	}

	// 一条 SSH 报出每对是否齐整、内容与库里的哈希是否一致。输出稳定按 cert
	// 路径排序,免得测试跟实现抢顺序。文件不在时 sha256sum 出错没关系,
	// ok/missing/diff 三种标记把状态说清了。
	list := make([]pair, 0, len(pairs))
	for _, p := range pairs {
		list = append(list, p)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].cert < list[j].cert })
	var probe strings.Builder
	for i, p := range list {
		wantCert := ""
		wantKey := ""
		if p.certPEM != "" {
			wantCert = sha256Hex([]byte(p.certPEM))
			wantKey = sha256Hex([]byte(p.keyPEM))
		}
		// 托管证书看内容哈希;非托管只看存在性。三态输出:
		//   ok:N        文件齐,且(若托管)内容一致
		//   missing:N   至少一个文件不在
		//   diff:N      文件齐但内容与库里的哈希对不上(仅托管)
		fmt.Fprintf(&probe,
			"test -f %[1]s -a -f %[2]s || { echo missing:%[5]d; }; "+
				"{ test -f %[1]s -a -f %[2]s; } && "+
				"{ test -n %[3]q && test \"$(sha256sum %[1]s | cut -d' ' -f1),$(sha256sum %[2]s | cut -d' ' -f1)\" != %[3]q,%[4]q && echo diff:%[5]d || echo ok:%[5]d; }; ",
			shellQuote(p.cert), shellQuote(p.key), wantCert, wantKey, i)
	}
	out, err := r.Run(ctx, n, probe.String())
	if err != nil {
		return false, fmt.Errorf("检查节点上的证书文件失败: %w", err)
	}
	type state int
	const (
		stMissing state = iota
		stOK
		stDiff
	)
	states := map[int]state{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if idx, ok := strings.CutPrefix(line, "missing:"); ok {
			if v, e := strconv.Atoi(idx); e == nil {
				states[v] = stMissing
			}
		} else if idx, ok := strings.CutPrefix(line, "diff:"); ok {
			if v, e := strconv.Atoi(idx); e == nil {
				states[v] = stDiff
			}
		} else if idx, ok := strings.CutPrefix(line, "ok:"); ok {
			if v, e := strconv.Atoi(idx); e == nil {
				states[v] = stOK
			}
		}
	}

	// 备材料:托管的直接用库里的 PEM;非托管缺失的按 SNI 签一对自签(同一
	// SNI 只签一对)。SNI 空时签匿名 CN 的对,让它在「不校验」的客户端里也
	// 能握手 —— 空 SNI 的入站渲染层允许。
	bySNI := map[string][2][]byte{}
	for i, p := range list {
		st := states[i]
		var material [2][]byte
		managed := p.certPEM != ""
		switch {
		case managed:
			// 托管:missing 与 diff 都以库里的内容覆盖。
			if st == stOK {
				continue
			}
			material = [2][]byte{[]byte(p.certPEM), []byte(p.keyPEM)}
		default:
			if st == stOK {
				continue
			}
			sni := p.sni
			if sni == "" {
				sni = "localhost"
			}
			m, ok := bySNI[sni]
			if !ok {
				certPEM, keyPEM, genErr := model.GenerateSelfSignedCertificate(sni)
				if genErr != nil {
					return reload, fmt.Errorf("为 SNI %q 签发自签证书失败: %w", sni, genErr)
				}
				m = [2][]byte{certPEM, keyPEM}
				bySNI[sni] = m
			}
			material = m
		}
		if err := PutFileMode(ctx, r, n, p.cert, material[0], "644"); err != nil {
			return reload, fmt.Errorf("安装证书 %s 失败: %w", p.cert, err)
		}
		if err := PutFileMode(ctx, r, n, p.key, material[1], "600"); err != nil {
			return reload, fmt.Errorf("安装私钥 %s 失败: %w", p.key, err)
		}
		if managed {
			reload = true
		}
	}
	return reload, nil
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return fmt.Sprintf("%x", sum)
}
