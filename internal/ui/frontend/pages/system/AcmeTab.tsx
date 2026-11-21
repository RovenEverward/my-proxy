import { useEffect, useRef, useState, type FormEvent, type ReactNode } from "react";
import { Field } from "../../components/primitives";
import { Button } from "../../components/ui/button";
import { useApp } from "../../runtime/app-context";
import { api, write } from "../../runtime/api";
import { sinceText } from "../../domain/settings";
import { dateTime } from "../../domain/nodes";

// 「域名与证书」tab(UC-26):管 https://<域名>/admin 的入场券。
// 域名托管在 Cloudflare,证书由控制面走 DNS-01 自己签(不用开 80 端口):
//   PUT /api/acme/config  改域名 / CF token / staging（给出才改，token 留空不动）
//   DELETE /api/acme/config  整个关掉(证书留在库里,配回同域名仍有效)
//   POST /api/acme/issue + GET /api/acme/issue?since=  单飞签发任务与事件流
//   GET /api/acme/status  配置 + 证书 + 最近一次签发结果
// 改域名或切换 CA 会让库里那张证书作废(server 侧保证),页面上先把这点说清楚。

export interface ACMEStatus {
  domain?: string;
  cf_token_set?: boolean;
  staging?: boolean;
  cert_issued?: boolean;
  cert_not_after?: string | null;
  cert_issued_at?: string | null;
  last_error?: string;
  last_attempt_at?: string | null;
  next_retry_at?: string | null;
  issue_running?: boolean;
  issue_state?: string;
}
interface IssueEvent { seq: number; msg: string }
interface IssueProgress { job?: number; state?: string; seq?: number; events?: IssueEvent[] }

const POLL_MS = 1500;
const POLL_TIMEOUT_MS = 15 * 60 * 1000;
const DAY = 86400 * 1000;

export function AcmeTab({ status, refresh }: { status: ACMEStatus; refresh: () => Promise<void> }) {
  const { can } = useApp();
  const writable = can("PUT", "/api/acme/config");
  const deletable = can("DELETE", "/api/acme/config");
  return <div className="grid g2">
    <AcmeConfigForm status={status} refresh={refresh} writable={writable} deletable={deletable} />
    <AcmeStatusCard status={status} refresh={refresh} writable={writable} />
  </div>;
}

function AcmeConfigForm({ status, refresh, writable, deletable }: { status: ACMEStatus; refresh: () => Promise<void>; writable: boolean; deletable: boolean }) {
  const { confirmBox, toast } = useApp();
  const [domain, setDomain] = useState(status.domain || "");
  const [token, setToken] = useState("");
  const [staging, setStaging] = useState(!!status.staging);
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);
  const lock = useRef(false), mounted = useRef(true), dirty = useRef(false);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  // 外部刷新(签发完成等)把状态送回来时,表单没动过就跟着更新。
  useEffect(() => { if (!dirty.current) { setDomain(status.domain || ""); setStaging(!!status.staging); } }, [status]);

  const domainChanged = domain.trim() !== (status.domain || "");
  const stagingChanged = staging !== !!status.staging;
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (lock.current || !writable) return;
    if (!domain.trim()) { setError("管理台域名不能留空；要整个关掉请用「关闭 ACME」"); return; }
    const body: { domain?: string; cf_token?: string; staging?: boolean } = {};
    if (domainChanged) body.domain = domain.trim();
    if (token.trim()) body.cf_token = token.trim();
    if (stagingChanged) body.staging = staging;
    if (!Object.keys(body).length) { toast("没有改动"); return; }
    lock.current = true; setSaving(true); setError("");
    try {
      await write("PUT", "/api/acme/config", body);
      toast(domainChanged && status.cert_issued ? "已保存；域名变了，原证书已作废，记得重新签发" : "已保存域名与证书设置");
      if (mounted.current) { setToken(""); dirty.current = false; }
      await refresh();
    } catch (cause) { if (mounted.current) setError(cause instanceof Error ? cause.message : String(cause)); }
    finally { lock.current = false; if (mounted.current) setSaving(false); }
  };

  const disable = () => confirmBox({
    title: "关闭域名与证书(ACME)?",
    text: "管理台回到自签证书：浏览器会提示证书不受信任。已签发的证书留在库里，再配回同域名可以直接用。",
    ok: "关闭 ACME", danger: true,
    run: async () => { await write("DELETE", "/api/acme/config"); toast("已关闭 ACME；现有证书保留，配回同域名仍可用"); dirty.current = false; await refresh(); },
  });

  const edit = (setter: (value: string) => void) => (event: { target: { value: string } }) => { dirty.current = true; setter(event.target.value); setError(""); };
  return <form className="card pad18" id="acme-form" noValidate onSubmit={submit}>
    <b className="b6">域名与证书</b>
    <p className="sub mt6 mb14">管理台的公网地址是 https://&lt;域名&gt;/admin。域名要托管在 Cloudflare，证书由控制面走 DNS-01 自己签，不用开 80 端口。</p>
    <Field label="管理台域名" htmlFor="acme-domain" hint="例如 cp.example.net；改了域名原证书作废，要重新签发">
      <input className="in mono" id="acme-domain" name="domain" autoComplete="off" spellCheck={false}
        value={domain} onChange={edit(setDomain)}
        placeholder="cp.example.net" readOnly={!writable || saving} />
    </Field>
    <Field label="Cloudflare API token" htmlFor="acme-token"
      hint={status.cf_token_set ? "已保存；要换就填新的，留空不动；需要 Zone.DNS 编辑权限" : "需要 Zone.DNS 编辑权限；存进控制面数据库，不回显"}>
      <input className="in mono" id="acme-token" name="cf_token" type="password" autoComplete="new-password"
        value={token} onChange={edit(setToken)}
        placeholder={status.cf_token_set ? "已保存，留空不动" : "粘贴 CF API token"} readOnly={!writable || saving} />
    </Field>
    <label className="chk"><input type="checkbox" id="acme-staging" checked={staging}
      onChange={event => { dirty.current = true; setStaging(event.target.checked); }} disabled={!writable || saving} />
      用 ACME staging 环境签发（调试用；浏览器不认它的证书，但不受 CA 速率限制）</label>
    <div className="err" id="acme-err" role="alert">{error}</div>
    <div className="flex8 end mt">
      {status.domain && deletable && <Button data-act="acme-off" disabled={saving} onClick={disable}>关闭 ACME</Button>}
      {writable && <Button type="submit" variant="primary" id="acme-save" loading={saving}>保存</Button>}
    </div>
    <p className="sub mt8">保存只是写下配置；要点右边「立即签发」才真的去拿证书。域名或 CA 一变，库里那张证书立刻作废。</p>
  </form>;
}

function AcmeStatusCard({ status, refresh, writable }: { status: ACMEStatus; refresh: () => Promise<void>; writable: boolean }) {
  const { toast } = useApp();
  const [issuing, setIssuing] = useState(false);
  const [events, setEvents] = useState<IssueEvent[]>([]);
  const [done, setDone] = useState<"" | "done" | "failed">("");
  const timer = useRef(0), mounted = useRef(true), since = useRef(0), deadline = useRef(0);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; clearTimeout(timer.current); }; }, []);

  const poll = () => {
    const tick = async () => {
      try {
        const p = await api<IssueProgress>("/api/acme/issue?since=" + since.current);
        if (!mounted.current) return;
        if (p.events?.length) { since.current = p.seq ?? since.current; setEvents(prev => [...prev, ...p.events!]); }
        if (p.state === "done" || p.state === "failed") {
          setDone(p.state); setIssuing(false);
          toast(p.state === "done" ? "证书签发完成，管理台 https 已生效" : "证书签发失败，详见上方事件", p.state === "failed");
          await refresh();
          return;
        }
      } catch { /* 抖一下下轮再试 */ }
      if (!mounted.current) return;
      if (Date.now() > deadline.current) {
        setDone("failed"); setIssuing(false); toast("等签发结果超时；到服务器上看控制面日志确认", true);
        return;
      }
      timer.current = window.setTimeout(tick, POLL_MS);
    };
    void tick();
  };

  const issue = async () => {
    if (issuing) return;
    setIssuing(true); setDone(""); setEvents([]); since.current = 0; deadline.current = Date.now() + POLL_TIMEOUT_MS;
    try {
      await write("POST", "/api/acme/issue");
      poll();
    } catch (cause) {
      setIssuing(false);
      toast(cause instanceof Error ? cause.message : String(cause), true);
    }
  };

  const daysLeft = status.cert_not_after ? Math.ceil((+new Date(status.cert_not_after) - Date.now()) / DAY) : 0;
  const row = (label: string, value: ReactNode) => <div className="row"><span className="sub w18">{label}</span><b className="grow">{value}</b></div>;
  return <div className="card pad18">
    <b className="b6">证书状态</b>
    <div className="mt6">
      {row("当前域名", status.domain ? <span className="mono">{status.domain}</span> : <span className="sub">未配置</span>)}
      {row("CA", status.staging ? "staging（测试）" : "Let's Encrypt（正式）")}
      {row("证书", status.cert_issued
        ? <span className={daysLeft <= 14 ? "c-warn" : ""}>至 {status.cert_not_after ? dateTime(status.cert_not_after) : "—"}{daysLeft > 0 ? `(剩 ${daysLeft} 天)` : "(已过期)"}</span>
        : <span className="sub">还没有证书</span>)}
      {status.cert_issued_at ? row("上次签发", sinceText(status.cert_issued_at)) : null}
      {status.next_retry_at ? row("下次续签", dateTime(status.next_retry_at)) : null}
      {status.last_error ? <div className="row"><span className="sub w18">上次错误</span><span className="c-bad grow">{status.last_error}</span></div> : null}
    </div>
    {events.length > 0 && <div className="mt8 mono sub" data-act="acme-events">
      {events.map(e => <div key={e.seq} className="upd-ev">{e.msg}</div>)}
    </div>}
    {issuing && !events.length && <p className="sub mt8">正在签发，DNS-01 要等公网 TXT 生效，通常十几秒…</p>}
    {done === "done" && <p className="sub mt8 c-ok">签发完成。管理台现在可以直接用 https://{status.domain}/admin 打开。</p>}
    <div className="flex8 end mt">
      <Button variant="primary" data-act="acme-issue" loading={issuing} disabled={!writable || !status.domain || !status.cf_token_set}
        onClick={() => void issue()}>{status.cert_issued ? "重新签发" : "立即签发"}</Button>
    </div>
    {(!status.domain || !status.cf_token_set) && <p className="sub mt8">先填好域名和 Cloudflare token 再签发。</p>}
  </div>;
}
