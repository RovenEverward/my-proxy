package node

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// EnsureTLSCertificates 保证节点上每个 TLS 入站引用的 (cert,key) 都存在。
//
// 这是「装得上」的那半步:sing-box / xray 校验配置时会去读证书文件,缺文件
// 的 sync 在 sing-box check 阶段就挂掉。在控制面签好、经 SSH 原子写下去,
// 让「新建 TLS 入站 → sync」不再需要操作者登到节点上跑 openssl。
//
// 三件事按这个顺序做:
//  1. 一条 SSH 把全部引用到的文件查一遍,找出缺失的对;
//  2. 对缺失的对,按 SNI 在控制面签自签证书(同一 SNI 只签一对,写进引用它的
//     每个路径 —— SNI 相同即同一份身份,无谓多签只会多几份文件);
//  3. PutFile 先写 .tmp 再 mv,写完 chmod 成约定权限。cert 0644(sing-box 以
//     自己的账户跑时要能读)、key 0600。
//
// 两文件都在的对直接跳过:节点上已有的证书(不管是上次我们装的还是操作者
// 自己放的)一律不覆盖。只有其中一个在时整对重签 —— 半个对没法用,
// 而「文件在但证书坏了」也不修:那属于人为干预,留给巡检报出来让人看。
func EnsureTLSCertificates(ctx context.Context, r Runner, n model.Node, inbounds []model.Inbound) error {
	type pair struct {
		cert, key string
		sni       string
	}
	pairs := make(map[string]pair) // cert 路径 → pair(cert 路径即 key 的唯一标识)
	for _, in := range inbounds {
		if in.Security != model.SecurityTLS || in.CertFile == "" || in.KeyFile == "" {
			continue
		}
		pairs[in.CertFile] = pair{cert: in.CertFile, key: in.KeyFile, sni: in.SNI}
	}
	if len(pairs) == 0 {
		return nil
	}

	// 一条 SSH 报出每对是否齐整。输出稳定按 cert 路径排序,免得测试跟实现抢顺序。
	list := make([]pair, 0, len(pairs))
	for _, p := range pairs {
		list = append(list, p)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].cert < list[j].cert })
	var probe strings.Builder
	for i, p := range list {
		probe.WriteString("test -f " + shellQuote(p.cert) + " -a -f " + shellQuote(p.key) + " && echo ok:" + strconv.Itoa(i) + " || echo missing:" + strconv.Itoa(i) + "; ")
	}
	out, err := r.Run(ctx, n, probe.String())
	if err != nil {
		return fmt.Errorf("检查节点上的证书文件失败: %w", err)
	}
	missing := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if idx, ok := strings.CutPrefix(line, "missing:"); ok {
			missing[idx] = true
		}
	}

	// 按 SNI 签一对,写进引用它的每个路径。SNI 空时签一个匿名 CN 的对,
	// 让它在「不校验」的客户端里也能握手 —— 空 SNI 的入站渲染层允许。
	bySNI := map[string][2][]byte{}
	for i, p := range list {
		if !missing[strconv.Itoa(i)] {
			continue
		}
		sni := p.sni
		if sni == "" {
			sni = "localhost"
		}
		material, ok := bySNI[sni]
		if !ok {
			certPEM, keyPEM, genErr := model.GenerateSelfSignedCertificate(sni)
			if genErr != nil {
				return fmt.Errorf("为 SNI %q 签发自签证书失败: %w", sni, genErr)
			}
			material = [2][]byte{certPEM, keyPEM}
			bySNI[sni] = material
		}
		if err := PutFileMode(ctx, r, n, p.cert, material[0], "644"); err != nil {
			return fmt.Errorf("安装证书 %s 失败: %w", p.cert, err)
		}
		if err := PutFileMode(ctx, r, n, p.key, material[1], "600"); err != nil {
			return fmt.Errorf("安装私钥 %s 失败: %w", p.key, err)
		}
	}
	return nil
}
