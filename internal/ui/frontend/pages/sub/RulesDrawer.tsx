import { NativeSelect } from "@/components/native-select";
import { useLayoutEffect, useRef, useState } from "react";
import { DrawerBody, DrawerFooter, DrawerHeader, Icon } from "../../components/primitives";
import { Button } from "../../components/ui/button";
import { useApp } from "../../runtime/app-context";
import type { ClientRule } from "../../runtime/types";
import { CLIENT_APPS, FORMATS, FORMAT_ORDER, describePattern, parsePattern, ruleHit } from "../../domain/settings";

interface RuleRow { key: number; rule: ClientRule }
export function RulesDrawer({ rules: initial, builtin, onDone }: { rules: ClientRule[]; builtin: ClientRule[]; onDone: (rules: ClientRule[]) => void }) {
  const { closeOverlays } = useApp();
  const key = useRef(initial.length);
  const [rows, setRows] = useState<RuleRow[]>(() => initial.map((rule, index) => ({ key: index, rule: { ...rule } })));
  const [ua, setUa] = useState("");
  const [focusKey, setFocusKey] = useState<number | null>(null);
  const rules = rows.map(row => row.rule), hit = ruleHit(rules, ua);
  const bad = rules.some(rule => { const parsed = parsePattern(rule.pattern); return !parsed.all && !parsed.body.trim(); });
  useLayoutEffect(() => {
    if (focusKey === null) return;
    const input = document.getElementById("rd-body")?.querySelector<HTMLInputElement>(`[data-rule-key="${focusKey}"]`);
    input?.closest(".rd-row")?.scrollIntoView({ block: "center" }); input?.focus(); setFocusKey(null);
  }, [focusKey, rows]);
  const change = (index: number, transform: (rule: ClientRule) => ClientRule) => setRows(previous => previous.map((row, at) => at === index ? { ...row, rule: transform(row.rule) } : row));
  const move = (index: number, target: number) => setRows(previous => { const next = [...previous], [row] = next.splice(index, 1); next.splice(target, 0, row); return next; });
  const add = () => {
    const newKey = key.current++;
    setRows(previous => { const next = [...previous], index = next.findIndex(row => parsePattern(row.rule.pattern).all); next.splice(index < 0 ? next.length : index, 0, { key: newKey, rule: { pattern: "(?i)", result: "clash-meta" } }); return next; });
    setFocusKey(newKey);
  };
  const result = hit >= 0 && hit < rules.length ? rules[hit] : undefined;
  return <><DrawerHeader title="识别规则" description="客户端拉订阅时会带上自己的标识（User-Agent），例如 clash-verge/v2.0.3。按它决定给哪种格式。" onClose={closeOverlays} />
    <DrawerBody className="rd-db" id="rd-body">
      <div className="field rd-test"><label htmlFor="rd-ua">输入客户端标识试试</label><input className="in mono" id="rd-ua" value={ua} onChange={event => setUa(event.target.value)} placeholder="例如 clash-verge/v2.0.3" autoComplete="off" spellCheck={false} />
        <div className="b5-try"><span className="h">常见的：</span>{["Clash Verge", "Stash", "Shadowrocket", "sing-box"].map(name => { const sample = CLIENT_APPS.find(([app]) => app === name)!; return <Button key={name} size="sm" className={"ton " + (ua === sample[1] ? "on" : "")} data-act="b5-try" data-ua={sample[1]} onClick={() => setUa(sample[1])}>{name}</Button>; })}</div>
        <div id="rd-hit">{!ua ? <span className="h">粘一段客户端标识，看它会拿到什么。</span> : <>{result ? <span className="b5-res">会拿到 <b>{FORMATS[result.result]?.[0] || result.result}</b>，对上的是「{describePattern(result.pattern)}」这条。</span> : <span className="b5-res">一条都没对上，会拿到 <b>节点链接</b>。</span>}<span className="sub" id="rd-ruling">实际以控制面判断为准</span></>}</div>
      </div><p className="b5-dlead">从上往下比，用第一条对上的。</p>
      <div className="rd-list b5-rl">{rows.map(({ key: rowKey, rule }, index) => {
        const parsed = parsePattern(rule.pattern), error = !parsed.all && !parsed.body.trim() ? "要填一段标识" : "";
        return <div key={rowKey} className={"rd-row b5-rr " + (index === hit ? "hit" : "")} data-i={index}>
          <div className="b5-rr-w">{parsed.all ? <span className="b5-k b5-all">其他所有客户端</span> : <><span className="b5-k">{parsed.regexp ? "客户端标识符合" : "客户端标识里含"}</span><input className="in mono" data-rp={index} data-rule-key={rowKey} value={parsed.body} aria-label="客户端标识里含的文字" aria-invalid={!!error} spellCheck={false} onChange={event => { const pattern = (parsed.ci ? "(?i)" : "") + event.target.value; change(index, current => { const next = { ...current, pattern }; if (pattern !== current.pattern) delete next.label; return next; }); }} /></>}
            <span className={"tag lab b5-on " + (index === hit ? "" : "off")} aria-hidden={index !== hit || undefined}>对上了</span></div>
          <div className="b5-rr-f"><span className="b5-k">拿到</span><NativeSelect className="in" data-rf={index} aria-label="拿到的格式" value={rule.result} onChange={event => change(index, current => ({ ...current, result: event.target.value }))}>{FORMAT_ORDER.map(format => <option key={format} value={format}>{FORMATS[format]?.[0] || format}</option>)}</NativeSelect>
            <span className="rd-ops"><Button size="icon-sm" data-act="rd-mv" data-i={index} data-to={index - 1} disabled={!index} aria-label="上移" title="上移" onClick={() => move(index, index - 1)}><Icon name="chevup" /></Button><Button size="icon-sm" data-act="rd-mv" data-i={index} data-to={index + 1} disabled={index >= rows.length - 1} aria-label="下移" title="下移" onClick={() => move(index, index + 1)}><Icon name="down" /></Button><Button size="icon-sm" data-act="rd-rm" data-i={index} aria-label="删除" title="删除" onClick={() => setRows(previous => previous.filter(row => row.key !== rowKey))}><Icon name="trash" /></Button></span></div>
          {error ? <span className="rd-err">{error}</span> : rule.label ? <span className="h b5-lbl">内置 · {rule.label}</span> : parsed.all ? <span className="h b5-lbl">上面几条都没对上的，都按这条给</span> : parsed.regexp ? <span className="h b5-lbl">这条按正则写的，保存时由控制面检查</span> : parsed.body.includes("|") ? <span className="h b5-lbl">「|」表示「或」</span> : null}
        </div>;
      })}{!rules.some(rule => parsePattern(rule.pattern).all) && <div className="rd-row b5-rr rd-last"><div className="b5-rr-w"><span className="b5-k b5-all">其他所有客户端</span></div><div className="b5-rr-f"><span className="b5-k">拿到</span><span className="rd-fixed">节点链接</span><span className="rd-ops" /></div></div>}</div>
      <Button size="sm" className="ton" data-act="rd-add" onClick={add}><Icon name="plus" />加一条</Button>
    </DrawerBody>
    <DrawerFooter><Button variant="ghost" data-act="rd-reset" onClick={() => setRows(builtin.map(rule => ({ key: key.current++, rule: { ...rule } })))}><Icon name="undo" />恢复内置</Button><span className="sp" /><span className="df-why" id="rd-why">{bad && <><Icon name="warn" />有一条还没填标识</>}</span><Button data-act="close" onClick={closeOverlays}>取消</Button><Button variant="primary" data-act="rd-done" id="rd-done" disabled={bad} onClick={() => { onDone(structuredClone(rules.length ? rules : builtin)); closeOverlays(); }}>完成</Button></DrawerFooter>
  </>;
}
