import { inbTitle, type ConnectionInbound } from "../../domain/connections";
import type { NodeView } from "../../domain/nodes";
const TARGET: Record<string, string> = { netflix: "Netflix", youtube: "YouTube", chatgpt: "ChatGPT", openai: "ChatGPT", disney: "Disney+", "disney+": "Disney+" };
const STATUS: Record<string, string> = { unknown: "没测出结果", unavailable: "不能用", failed: "没通过", partial: "只通过了一部分", skipped: "跳过了", canceled: "被中断了", cancelled: "被中断了", timeout: "超时了" };
const NETWORK: Record<string, string> = { DNS: "域名解析", ICMP: "ping", HTTP: "网页请求", 路由: "路由追踪" };
export function plainSummary(lines: string[], node: NodeView, inbounds: ConnectionInbound[]): string[] {
  const connection = (id: string) => { const inbound = inbounds.find(value => value.id === Number(id)); return inbound ? `「${inbTitle(inbound, node)}」` : "一个连接方式"; };
  const status = (value: string) => STATUS[value] || "没通过";
  const rules: [RegExp, (match: RegExpMatchArray) => string][] = [
    [/^融合怪 \S+: (ok|success|done|completed|finished)$/i, () => ""], [/^融合怪 \S+: (canceled|cancelled)$/i, () => "完整测评被中断，没有跑完"],
    [/^融合怪 \S+: /, () => "完整测评没有跑完"], [/^融合怪测评未完成/, () => "完整测评没有跑完"], [/^融合怪缺少可用测速结果/, () => "完整测评没测出网速，另外补测了一次"],
    [/^独立速度补测已完成/, () => "补测网速已完成"], [/^独立速度补测未运行/, () => "时间用完了，没有补测网速"], [/^独立速度补测未完成/, () => "补测网速没有完成"],
    [/^入站 (\d+)\/入口 \d+ (\S+) 本次实测 (\S+):/, match => `通过${connection(match[1])}访问 ${TARGET[match[2].toLowerCase()] || match[2]}：${status(match[3])}`],
    [/^入站 (\d+)\/入口 \d+ 检测失败/, match => `${connection(match[1])}的连通检测没通过`], [/^入站 (\d+) 证书检查失败/, match => `${connection(match[1])}的证书检查没通过`],
    [/^入站 (\d+) 证书剩余 (-?\d+) 天/, match => Number(match[2]) > 0 ? `${connection(match[1])}的证书还剩 ${match[2]} 天` : `${connection(match[1])}的证书已过期`],
    [/^节点代理服务未运行/, () => "节点上的代理程序没在运行"], [/^节点实测配置哈希与最后下发的配置不一致/, () => "节点上的配置和控制面不一致"],
    [/^节点没有已下发配置哈希/, () => "还没把配置应用到节点，没法判断配置是否一致"], [/^(节点配置哈希未读到|读取配置漂移基准失败)/, () => "没读到节点上的配置，没法判断配置是否一致"],
    [/^(xray|sing-box) 服务未运行/, match => `${match[1]} 没在运行`], [/^(xray|sing-box) 检查失败/, match => `${match[1]} 检查没通过`], [/^sing-box 配置漂移检查/, () => "sing-box 的配置没法确认是否一致"],
    [/^节点地址在体检期间变化/, () => "体检期间节点地址变了，结果可能不准"], [/^网络检测未完成/, () => "网络检测没有跑完"], [/^测评未运行/, () => "这次没有跑测评"],
    [/^体检中断/, () => "体检中途被打断，已保存做完的部分"], [/^网络检测工具不可用: (.+)$/, match => `节点上缺少检测工具「${match[1]}」`],
    [/^(\S+) (\S+) 测速 (\S+):/, match => `测网速（${match[1]}）${status(match[3])}`], [/^(IPv4|IPv6|ipv4|ipv6) IP 检测 (\S+):/, match => `${match[1].toUpperCase().replace("IPV", "IPv")} 地址检测${status(match[2])}`],
    [/^(.+) (DNS|ICMP|HTTP|路由) (\S+):/, match => `访问 ${match[1]} 的${NETWORK[match[2]]}${status(match[3])}`],
    [/^读取入站失败/, () => "读不到这台节点的连接方式"], [/^节点没有配置入站/, () => "节点还没有连接方式，没做连通检测"], [/^报告超过 256 KiB/, () => "报告太长，部分细节没有保存"],
  ];
  const output = [...new Set(lines.filter(Boolean).map(line => { const rule = rules.find(([pattern]) => pattern.test(line)); return rule ? rule[1](line.match(rule[0])!) : "有一项检查没通过，原话见「详情」"; }).filter(Boolean))];
  return output.includes("完整测评被中断，没有跑完") ? output.filter(line => line !== "完整测评没有跑完") : output;
}
