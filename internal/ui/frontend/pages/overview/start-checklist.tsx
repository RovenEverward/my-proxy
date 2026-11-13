import { useState } from "react";
import { useApp } from "@/runtime/app-context";
import { Button } from "@/components/ui/button";
import { FlagText, Icon } from "@/components/primitives";
import { nodeLabel } from "@/domain/nodes";
import { clientLabel } from "@/domain/users";
import type { OverviewData } from ".";

interface Step { title: string; done: boolean; detail: string; href?: string; action?: string; skip?: boolean; icon?: string; button?: string }
export function useChecklist(data: OverviewData | null) {
  const { meta, can } = useApp();
  const storageKey = (key: string) => `mpx-cl-${key}:${meta.server || "-"}`;
  const get = (key: string) => { try { return localStorage.getItem(storageKey(key)) === "1"; } catch { return false; } };
  const [skipped, setSkipped] = useState(() => get("skip-sub"));
  const [hidden, setHidden] = useState(() => get("hide"));
  const [open, setOpen] = useState(false);
  const put = (key: string) => { try { localStorage.setItem(storageKey(key), "1"); } catch { /* Keep the current session state. */ } };
  const online = data?.nodes.find(node => node.status?.reachable);
  const user = data?.users.users.slice().sort((a, b) => +new Date(a.created_at || 0) - +new Date(b.created_at || 0))[0];
  const pull = data?.pulls?.slice().sort((a, b) => +new Date(a.first_pulled_at) - +new Date(b.first_pulled_at))[0];
  const steps: Step[] = !data ? [] : [
    { title: "第一个节点在线", done: !!online, detail: online ? `${nodeLabel(online)} 在线` : data.nodes.length ? "节点还连不上。打开节点看看哪里出了问题" : "装好控制面时会顺带建好第一个节点；也可以在「节点」里添加", href: "#nodes", button: "查看节点" },
    { title: "设好订阅地址", done: !!data.settings.sub_url_prefix || skipped, detail: data.settings.sub_url_prefix || (skipped ? "先用服务器 IP，之后在「订阅」里随时能改" : "填你给用户用的域名。不设的话，链接里会直接露出服务器 IP"), href: "#sub", button: "去设置", skip: true },
    { title: "添加第一个用户", done: data.users.total > 0, detail: user?.username || "每个用户一条自己的订阅，可以单独停用和续期", action: can("POST", "/api/users") ? "add-user" : undefined, button: "添加用户", icon: "plus" },
    { title: "把订阅发给他", done: data.pulls ? !!pull : data.users.total > 0, detail: pull ? `${clientLabel(pull.client)} 已经导入` : data.pulls === null ? "控制面版本较旧，读不到拉取记录" : user ? `给 ${user.username} 扫码或发链接；他的客户端第一次拉取订阅后，这一步自动完成` : "建好用户后，在这里扫码或复制链接", href: user ? "#users/" + encodeURIComponent(user.username) : undefined, button: user ? "发给 " + user.username : undefined, icon: "qr" },
  ];
  return { steps, hidden, open, toggle: () => setOpen(value => !value), hide: () => { put("hide"); setHidden(true); }, skip: () => { put("skip-sub"); setSkipped(true); } };
}
export function StartChecklist({ steps, onSkip, onAddUser }: { steps: Step[]; onSkip: () => void; onAddUser: () => void }) {
  const done = steps.filter(step => step.done).length, current = steps.findIndex(step => !step.done);
  return <section className="card cl" id="start"><div className="cl-h"><div><h3>开始使用</h3><p className="sub">{done === 0 ? "四步把第一条订阅发出去，大约 5 分钟" : done === steps.length ? "4 步都完成了" : `还差 ${steps.length - done} 步`}</p></div><span className="sp" /><span className="cl-n"><b>{done}</b> / {steps.length}</span></div><div className="cl-bar">{steps.map((step, index) => <i key={index} className={step.done ? "on" : ""} />)}</div>
    <ol className="cks">{steps.map((step, index) => <li key={step.title} className={"ck " + (step.done ? "done" : index === current ? "cur" : "")}><span className="ck-i">{step.done ? <Icon name="check" /> : index + 1}</span><div className="ck-t"><b>{step.title}</b><span className="sub"><FlagText>{step.detail}</FlagText></span></div><div className="ck-a">{!step.done && index === current && <>{step.skip && <Button size="sm" data-act="cl-skip" onClick={onSkip}>先用 IP</Button>}{step.action === "add-user" ? <Button size="sm" variant="primary" data-act="user-new" onClick={onAddUser}><Icon name="plus" />{step.button}</Button> : step.href && <a className="btn sm pri" href={step.href}>{step.icon && <Icon name={step.icon} />}{step.button}</a>}</>}</div></li>)}<li className="ck opt"><span className="ck-i"><Icon name="plus" /></span><div className="ck-t"><b>可选：让它更好用</b><span className="sub">随时都能做，不影响上面的进度</span></div><div className="ck-a"><a className="btn sm" href="#nodes"><Icon name="nodes" />再加一个节点</a><a className="btn sm" href="#rules"><Icon name="split" />调整分流</a><a className="btn sm" href="#system"><Icon name="bell" />打开到期提醒</a></div></li></ol></section>;
}
