import { Button } from "../../components/ui/button";
import { Icon } from "../../components/primitives";
import { useApp } from "../../runtime/app-context";
import { SEND_FORMATS, pullText, type Pull, type User, type UserLinks } from "../../domain/users";
import { DetailReadError } from "./components";
import type { DetailPart, UserDetailData } from "./hooks";
export function SendSubscription({ user, detail, pull, pullsKnown, retry }: { user: User; detail: UserDetailData; pull?: Pull; pullsKnown?: boolean; retry: (part: DetailPart) => void }) {
  const app = useApp(), links = detail.links, link = links?.auto || "";
  const copy = async (text: string) => {
    try { await navigator.clipboard.writeText(text); app.toast("已复制"); }
    catch { app.toast("复制没成功，请手动复制", true); }
  };
  if (!link) return <section className="send" id="send" aria-busy={detail.pending.links || undefined}><div className="send-h"><h3>发给他</h3></div>{detail.pending.links && <p className="sub">正在读取订阅链接…</p>}<DetailReadError label="订阅链接" part="links" error={detail.errors.links} onRetry={() => retry("links")} /></section>;
  const qr = `/local/qr.svg?d=${encodeURIComponent(link)}`;
  const warning = "链接里是服务器 IP，设好订阅地址后新旧链接都能用";
  return <section className="send" id="send"><div className="send-h"><h3>发给他</h3><span className="sub" id="send-pull">{pull ? pullText(pull) : pullsKnown ? "还没有客户端导入过" : ""}</span></div>
    <div className="send-b"><div className="qr-tile"><img className="qrs" src={qr} alt={`${user.username} 的订阅二维码`} /></div><div className="send-r"><Button variant="primary" className="send-copy" data-act="copy" data-text={link} onClick={() => void copy(link)}><Icon name="copy" />复制订阅链接</Button><div className="send-link mono" title={link}>{link.replace(/^https?:\/\//, "")}</div><a className="btn" href={qr} download={`${user.username}-订阅二维码.svg`}><Icon name="dl" />保存二维码</a><p className="send-hint">Clash Verge、Shadowrocket、sing-box、v2rayN 都能直接扫码或粘贴导入，会自动拿到适合它的格式。</p></div></div>
    <DetailReadError label="订阅链接" part="links" error={detail.errors.links} onRetry={() => retry("links")} /><DetailReadError label="订阅设置" part="settings" error={detail.errors.settings} onRetry={() => retry("settings")} />
    {detail.settings && !detail.settings.sub_url_prefix && <div className="send-ip"><span className="send-ip-t" title={warning}><Icon name="warn" />{warning}</span><a className="lnk u-link" href="#sub">去设置</a></div>}
    <details className="fmts"><summary><Icon name="arrow" className="car" />其他格式<span className="sub">客户端不认自动识别时再用</span></summary><div className="fmt-l">{SEND_FORMATS.filter(([key]) => links?.[key]).map(([key, name, hint]) => <div className="fmt" key={key}><div className="fmt-t"><b>{name}</b><span className="sub">{hint}</span></div><button type="button" className="ib" data-act="copy" data-text={links![key]} title={`复制 ${name} 链接`} aria-label={`复制 ${name} 链接`} onClick={() => void copy(links![key])}><Icon name="copy" /></button></div>)}</div></details>
  </section>;
}
