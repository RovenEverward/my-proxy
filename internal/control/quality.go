package control

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// ProbeNodeQuality 在节点上跑一次轻量 IP 质量探测,输出分段原文给控制面解析:
//
//	===MPX_IP===     出口 IPv4(api.ipify.org)
//	===MPX_GEO===    ipwho.is/<ip> 的地理 JSON
//	===MPX_FLAGS===  api.ipapi.is 的风险标记 JSON
//	===MPX_ABUSE===  AbuseIPDB 置信分 JSON(仅控制面配了密钥时)
//
// 单步失败只影响自己那一段:出口 IP 都没有才整段作废。abuseKey 与 ipapiKey
// 来自控制面环境变量,内联进脚本传到节点,不落盘、不进 URL。
func (c *Control) ProbeNodeQuality(ctx context.Context, n model.Node, abuseKey, ipapiKey string) (string, error) {
	flagsCmd := `curl -fsS --max-time 10 "https://api.ipapi.is/?q=$IP" 2>/dev/null || :`
	if ipapiKey != "" {
		keyJSON, _ := json.Marshal(ipapiKey)
		// 密钥走 POST body;BODY 由节点上的 printf 拼,格式串与 key 都是单引号字面量。
		flagsCmd = `BODY=$(printf '{"q":"%s","key":%s}' "$IP" ` + shellQuote(string(keyJSON)) + `) && curl -fsS --max-time 10 -X POST -H "Content-Type: application/json" --data-binary "$BODY" "https://api.ipapi.is/" 2>/dev/null || :`
	}
	script := `command -v curl >/dev/null 2>&1 || { echo "===MPX_NOCURL==="; exit 0; }
IP=$(curl -fsS --max-time 8 https://api.ipify.org 2>/dev/null || :)
echo "===MPX_IP==="; echo "$IP"
echo "===MPX_GEO==="
[ -n "$IP" ] && curl -fsS --max-time 8 "https://ipwho.is/$IP" 2>/dev/null || :
echo "===MPX_FLAGS==="
[ -n "$IP" ] && ` + flagsCmd
	if abuseKey != "" {
		script += `
echo "===MPX_ABUSE==="
[ -n "$IP" ] && curl -fsS --max-time 8 -H ` + shellQuote("Key: "+abuseKey) + ` "https://api.abuseipdb.com/api/v2/check?ipAddress=$IP&maxAgeInDays=90" 2>/dev/null || :`
	}
	out, err := c.runner.Run(ctx, n, script)
	if err != nil {
		return "", fmt.Errorf("节点上探测质量失败(需要 curl): %w", err)
	}
	return out, nil
}
