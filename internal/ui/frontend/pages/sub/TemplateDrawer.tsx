import { useEffect, useRef, useState, type FormEvent } from "react";
import { CodeEditor, type CodeEditorHandle } from "../../components/code-editor";
import { DrawerBody, DrawerFooter, DrawerHeader, ErrorState, Icon, LoadingTable } from "../../components/primitives";
import { Button } from "../../components/ui/button";
import { api, write } from "../../runtime/api";
import { useApp } from "../../runtime/app-context";
import { TEMPLATE_META, templateBytes, templateLines, templateSize, validateTemplate, type TemplateError, type TemplateFormat } from "../../domain/settings";

export function TemplateDrawer({ format, custom, onSaved }: { format: TemplateFormat; custom: boolean; onSaved: () => void }) {
  const { can, closeOverlays, confirmBox, toast } = useApp();
  const [original, setOriginal] = useState<string | null>(null), [text, setText] = useState("");
  const [loadError, setLoadError] = useState<Error | null>(null), [revision, setRevision] = useState(0);
  const [serverError, setServerError] = useState<TemplateError | null>(null), [saving, setSaving] = useState(false);
  const [position, setPosition] = useState([1, 1]);
  const editor = useRef<CodeEditorHandle>(null), mounted = useRef(true), savingRef = useRef(false);
  const metadata = TEMPLATE_META[format], writable = can("PUT", "/api/settings/templates/x");
  const dirty = original !== null && text !== original;
  const syntaxError = original === null ? null : validateTemplate(format, text), error = serverError || syntaxError;
  useEffect(() => {
    let active = true; mounted.current = true; setLoadError(null); setOriginal(null);
    void api<string>("/api/settings/templates/" + format, { asText: true, headers: { Accept: "text/plain" } }).then(value => { if (active) { setText(value); setOriginal(value); } }).catch((cause: Error) => { if (active) setLoadError(cause); });
    return () => { active = false; mounted.current = false; };
  }, [format, revision]);
  const change = (value: string) => { setText(value); setServerError(null); };
  const showError = (result: TemplateError | null) => { if (result?.line) editor.current?.mark(result.line); };
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (!writable || savingRef.current || original === null) return;
    const result = validateTemplate(format, text); if (result) { showError(result); return; }
    if (!dirty) return;
    savingRef.current = true; setSaving(true); setServerError(null);
    try {
      await api("/api/settings/templates/" + format, { method: "PUT", body: text, headers: { "Content-Type": "text/plain; charset=utf-8" } });
      toast(`已保存 ${format} 订阅底稿`); onSaved(); if (mounted.current) closeOverlays();
    } catch (cause) {
      const message = cause instanceof Error ? cause.message : String(cause), line = /line (\d+)/.exec(message);
      const result = { message, line: line ? +line[1] : undefined, server: true }; if (mounted.current) { setServerError(result); showError(result); }
    } finally { savingRef.current = false; if (mounted.current) setSaving(false); }
  };
  const restore = () => confirmBox({ title: `恢复 ${format} 为内置订阅底稿？`, text: `会删除你存的 ${format} 自定义模板(不可撤销，控制面不留历史);之后所有用户的 ${format} 订阅改用内置默认。想留底，先打开模板把内容复制出来。`, ok: "删除自定义底稿", danger: true, typed: format,
    run: async () => { await write("DELETE", "/api/settings/templates/" + format); toast(`已恢复 ${format} 为内置订阅底稿`); onSaved(); closeOverlays(); } });
  return <form id="tpl-form" noValidate onSubmit={submit}>
    <DrawerHeader title={<><span className="mono">{format}</span> 订阅底稿 <span className={"pill " + (custom ? "on" : "")}>{custom ? "自定义" : "内置默认"}</span></>} description={metadata.how} onClose={closeOverlays} />
    {loadError ? <DrawerBody><ErrorState title="订阅底稿" error={loadError} onRetry={() => setRevision(value => value + 1)} /></DrawerBody> : original === null ? <DrawerBody aria-busy="true"><LoadingTable rows={8} columns={2} /></DrawerBody> : <DrawerBody className="tpl-db">
      {!custom && <div className="callout stone mb8 tpl-note"><div>这是内置默认的副本。{writable && <>改动后保存，就成为 <b>{format}</b> 的自定义模板，对所有用户生效。</>}</div></div>}
      <CodeEditor id="tpl-ed" ref={editor} value={text} language={metadata.language} readOnly={!writable || saving} errorLine={error?.line} onChange={change} onCursorChange={(line, column) => setPosition([line, column])} />
      <div className="ed-s"><span className="tag">{metadata.label}</span><span id="tpl-pos">行 {position[0]}，列 {position[1]}</span><span id="tpl-size">{templateLines(text)} 行 · {templateSize(templateBytes(text))}</span><span id="tpl-dirty" className="c-warn">{dirty ? "● 未保存" : ""}</span><span className="sp" /><span id="tpl-ok">{syntaxError ? <span className="c-bad"><Icon name="warn" className="sm" />有错误</span> : <span className="c-ink"><Icon name="check" className="sm" />语法正确</span>}</span></div>
      <div className={"callout bad mt8 " + (error ? "" : "hidden")} id="tpl-err" role="alert">{error && <><Icon name="warn" /><div>{error.server && <b>服务端拒收：</b>}{error.message}{error.line && <> <button type="button" className="lnk" id="tpl-goto" onClick={() => { editor.current?.mark(error.line!); editor.current?.focus(); }}>跳到第 {error.line} 行</button></>}</div></>}</div>
    </DrawerBody>}
    <DrawerFooter>{custom && can("DELETE", "/api/settings/templates/x") && <Button variant="ghost" size="sm" data-act="tpl-del" data-f={format} disabled={saving} onClick={restore}>恢复内置</Button>}
      {writable && <><label className="btn sm ghost"><Icon name="up" />从文件读…<input type="file" id="tpl-file" accept={metadata.extension + ",.txt,.yml"} hidden disabled={original === null || saving} onChange={async event => { const input = event.currentTarget, file = input.files?.[0]; input.value = ""; if (!file) return; try { const value = await file.text(); if (mounted.current) { change(value); requestAnimationFrame(() => showError(validateTemplate(format, value))); } } catch (cause) { if (mounted.current) setServerError({ message: cause instanceof Error ? cause.message : String(cause) }); } }} /></label>
        {metadata.language === "json" && <Button variant="ghost" size="sm" id="tpl-fmt" disabled={original === null || saving} onClick={() => { try { change(JSON.stringify(JSON.parse(text), null, 2) + "\n"); } catch { showError(syntaxError); } }}>格式化</Button>}
        <Button variant="ghost" size="sm" id="tpl-revert" disabled={!dirty || saving} onClick={() => { if (original !== null) change(original); }}>撤销改动</Button></>}
      <span className="sp" /><Button data-act="close" onClick={closeOverlays}>关闭</Button>{writable && <Button type="submit" variant="primary" id="tpl-save" loading={saving} disabled={!dirty || !!syntaxError}>保存</Button>}
    </DrawerFooter>
  </form>;
}
