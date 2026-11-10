import { useEffect, useState } from "react";
import { Card } from "../../components/primitives";
import { api } from "../../runtime/api";
import { errorMessage, groupID, RULE_TYPES, ruleValueText, type RulesPageData, type RuleTestResult } from "../../domain/routing";
import { TargetPill } from "./components";

let rememberedDomain = "";
export function TestCard({ data, scope }: { data: RulesPageData; scope: string }) {
  const [domain, setDomain] = useState(rememberedDomain);
  const [result, setResult] = useState<RuleTestResult | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [immediate, setImmediate] = useState(false);
  const rulesKey = JSON.stringify(data.rules);
  useEffect(() => {
    rememberedDomain = domain;
    const controller = new AbortController(); let active = true;
    if (!domain.trim()) { setResult(null); setError(""); setLoading(false); return; }
    setResult(null); setError(""); setLoading(true);
    const timer = setTimeout(() => {
      void api<RuleTestResult>("/api/rules/test", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ domain: domain.trim(), user: scope }), signal: controller.signal }).then(value => { if (active) setResult(value); }).catch((reason: unknown) => { if (active) setError(errorMessage(reason)); }).finally(() => { if (active) setLoading(false); });
    }, immediate ? 0 : 350);
    return () => { active = false; clearTimeout(timer); controller.abort(); };
  }, [domain, scope, rulesKey, immediate]);
  const how = result && groupID(result.target) ? result.mode === "auto" ? `自动在 ${result.members?.length || 0} 个节点里挑最快的` : `你在客户端里选的那个(${result.members?.length || 0} 个可选)` : "";
  return <Card title="测试一个网址"><div className="pad16"><label htmlFor="rp-test" className="vh">测试网址</label><input className="in mono" id="rp-test" value={domain} placeholder="输入域名，比如 chatgpt.com" autoComplete="off" spellCheck={false} onChange={event => { setImmediate(false); setDomain(event.target.value); }} />
    <div className="chips mt6">{["chatgpt.com", "netflix.com", "bilibili.com", "github.com", "baidu.com"].map(value => <button type="button" className="tag rp-try" key={value} data-act="rp-try" data-d={value} onClick={() => { setImmediate(true); setDomain(value); }}>{value}</button>)}</div>
    <div id="rp-res" aria-live="polite" aria-busy={loading}>
      {error ? <p className="c-bad mt" role="alert">{error}</p> : !result ? <p className="sub mt">{loading ? "正在测试…" : <>看看它会命中哪条规则、最后从哪里出去。{scope ? `按 ${scope} 的规则算。` : ""}</>}</p> : <div className="rp-res"><div className="sub">{result.domain}</div><div className="rp-big"><TargetPill target={result.target} groups={data.groups} /></div>
        <dl className="kv flat"><dt>命中</dt><dd>{result.matched ? <>{result.matched.scope === "user" && <><span className="pill on">仅 {result.user}</span> </>}第 {result.matched.index} 条 · {RULE_TYPES[result.matched.rule.type]} <b>{ruleValueText(result.matched.rule, data.catalog)}</b></> : "没有规则对上，走「其他全部」"}</dd>
          {how && <><dt>实际出口</dt><dd>{how}{result.members?.length ? <div className="chips mt6">{result.members.map((name, index) => <span className="tag" key={index}>{name}</span>)}</div> : <> <span className="c-warn">· 组里没有节点，只剩直连</span></>}</dd></>}
          {!!result.uncertain?.length && <><dt>注意</dt><dd className="c-warn">{result.uncertain.map((match, index) => <div key={index}>第 {match.index} 条 {RULE_TYPES[match.rule.type]} {ruleValueText(match.rule, data.catalog)}:{match.reason}</div>)}</dd></>}
          {!!result.disabled_hits?.length && <><dt>停用</dt><dd className="sub">有 {result.disabled_hits.length} 条已停用的规则本来会命中</dd></>}
        </dl><p className="sub">规则组以 Clash 版上游规则集模拟;客户端实际下载的规则集可能稍有不同。</p></div>}
    </div>
  </div></Card>;
}
