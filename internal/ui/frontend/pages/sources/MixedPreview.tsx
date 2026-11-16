import { NativeSelect } from "@/components/native-select";
import { useEffect, useState } from "react";
import { Button } from "../../components/ui/button";
import { Field, FlagText, Icon } from "../../components/primitives";
import { useApp } from "../../runtime/app-context";
import { api } from "../../runtime/api";
import type { User } from "../../runtime/types";
import { errorMessage, SOURCE_FORMATS, type SourceFormat, type SubscriptionPreview } from "../../domain/routing";
import { SourceIssues } from "./components";
import { useWrittenRevision } from "./hooks";

let rememberedUser = "", rememberedFormat: SourceFormat = "clash-meta";

export function MixedPreviewModal({ users, revision }: { users: User[]; revision: string }) {
  const app = useApp();
  const [user, setUser] = useState(() => users.some(item => item.username === rememberedUser) ? rememberedUser : users[0]?.username || "");
  const [format, setFormat] = useState<SourceFormat>(rememberedFormat);
  const [result, setResult] = useState<SubscriptionPreview | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [retry, setRetry] = useState(0);
  const writtenRevision = useWrittenRevision();

  useEffect(() => {
    if (!users.some(item => item.username === user)) {
      const fallback = users[0]?.username || "";
      rememberedUser = fallback;
      setUser(fallback);
    }
  }, [users, user]);

  useEffect(() => {
    const controller = new AbortController(); let active = true;
    if (!user) { setResult(null); setError(""); setLoading(false); return; }
    setLoading(true); setError(""); setResult(null);
    void api<SubscriptionPreview>(`/api/users/${encodeURIComponent(user)}/subscription-preview?format=${encodeURIComponent(format)}`, { signal: controller.signal })
      .then(value => { if (active) setResult(value); })
      .catch((reason: unknown) => { if (active) setError(errorMessage(reason)); })
      .finally(() => { if (active) setLoading(false); });
    return () => { active = false; controller.abort(); };
  }, [user, format, revision, writtenRevision, retry]);

  return <div className="source-preview-modal">
    <div className="mbody">
      <div className="src-modal-head">
        <h2>混合订阅预览</h2>
        <span className="sp" />
        <Button size="icon" data-act="close" aria-label="关闭" onClick={app.closeModal}><Icon name="x" /></Button>
      </div>
      <p>按用户授权和客户端格式显示实际输出。用量只统计自建节点。</p>
      <div className="two">
        <Field label="接收用户" htmlFor="source-preview-user">
          <NativeSelect className={"in" + (loading ? " busy" : "")} id="source-preview-user" disabled={!users.length} value={user} aria-busy={loading || undefined} onChange={event => { rememberedUser = event.target.value; setUser(event.target.value); }}>
            <option value="">选择用户</option>
            {users.map(item => <option key={item.username} value={item.username}>{item.username}</option>)}
          </NativeSelect>
        </Field>
        <Field label="客户端格式" htmlFor="source-preview-format">
          <NativeSelect className={"in" + (loading ? " busy" : "")} id="source-preview-format" value={format} aria-busy={loading || undefined} onChange={event => { rememberedFormat = event.target.value as SourceFormat; setFormat(rememberedFormat); }}>
            {SOURCE_FORMATS.map(([value, label]) => <option key={value} value={value}>{label}</option>)}
          </NativeSelect>
        </Field>
      </div>
      <div id="source-preview" className="src-preview-box" aria-live="polite" aria-busy={loading || undefined}>
        {!users.length ? <p className="sub">先添加接收用户。</p>
          : !user ? <p className="sub">选择接收用户后查看。</p>
          : error ? <><p className="c-bad" role="alert">{error}</p><Button data-act="source-preview-retry" onClick={() => setRetry(previous => previous + 1)}>重试预览</Button></>
          : !result ? <p className="sub">正在读取预览…</p>
          : <>
            {result.status !== "active" && <p className="sub">用户当前不可用，订阅仅显示状态占位。</p>}
            {result.nodes.length ? result.nodes.map((node, index) => (
              <div className="row source-preview-row" key={`${node.source_id}:${index}`}>
                <div className="grow source-name">
                  <b><FlagText>{node.name}</FlagText></b>
                  <div className="sub">{node.source_name} · {node.protocol}</div>
                  {node.reasons?.map((reason, reasonIndex) => <div className="c-bad sub" key={reasonIndex}>{reason}</div>)}
                </div>
                <span className="sub">{node.groups.join("、")}</span>
              </div>
            )) : <p className="sub">当前没有可输出的节点。</p>}
            <SourceIssues issues={result.issues} />
          </>}
      </div>
    </div>
    <div className="mf">
      <a className="lnk" href="#rules/groups" onClick={app.closeModal}>管理策略组</a>
      <span className="sp" />
      <Button data-act="modal-close" onClick={app.closeModal}>关闭</Button>
    </div>
  </div>;
}
