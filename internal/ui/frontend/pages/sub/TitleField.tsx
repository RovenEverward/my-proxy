import { Fragment, useEffect, useLayoutEffect, useRef, useState } from "react";
import { Icon } from "../../components/primitives";

const TOKEN = "{USERNAME}";
export function titleValue(element: Node): string {
  let text = "";
  for (const child of element.childNodes) {
    if (child instanceof HTMLElement && child.dataset.tok) text += TOKEN;
    else if (child.nodeType === Node.TEXT_NODE) text += child.textContent || "";
    else text += titleValue(child);
  }
  return text.replace(/\n/g, "");
}
export function TitleField({ value, readOnly, onChange }: { value: string; readOnly: boolean; onChange: (value: string) => void }) {
  const ref = useRef<HTMLDivElement>(null);
  const [rendered, setRendered] = useState(value);
  const [revision, setRevision] = useState(0);
  const focusAt = useRef<number | null>(null);
  useEffect(() => {
    const element = ref.current;
    if (element && titleValue(element) !== value) { setRendered(value); setRevision(previous => previous + 1); }
  }, [value]);
  useLayoutEffect(() => {
    if (focusAt.current === null || !ref.current) return;
    let offset = focusAt.current;
    focusAt.current = null;
    ref.current.focus();
    const selection = getSelection(), range = document.createRange();
    range.selectNodeContents(ref.current); range.collapse(false);
    for (const node of ref.current.childNodes) {
      const token = node instanceof HTMLElement && !!node.dataset.tok;
      const length = token ? TOKEN.length : (node.textContent || "").length;
      if (offset <= length) {
        if (token) { if (offset === 0) range.setStartBefore(node); else range.setStartAfter(node); }
        else range.setStart(node, Math.min(offset, length));
        range.collapse(true); break;
      }
      offset -= length;
    }
    selection?.removeAllRanges(); selection?.addRange(range);
  }, [revision]);
  const display = (text: string, position?: number) => { focusAt.current = position ?? null; setRendered(text); setRevision(previous => previous + 1); };
  const insertToken = () => {
    const element = ref.current;
    if (!element || readOnly) return;
    const selection = getSelection();
    let next: string, position: number;
    if (selection?.rangeCount && element.contains(selection.anchorNode)) {
      const selected = selection.getRangeAt(0), before = selected.cloneRange(), after = selected.cloneRange();
      before.selectNodeContents(element); before.setEnd(selected.startContainer, selected.startOffset);
      after.selectNodeContents(element); after.setStart(selected.endContainer, selected.endOffset);
      const prefix = titleValue(before.cloneContents());
      next = prefix + TOKEN + titleValue(after.cloneContents()); position = prefix.length + TOKEN.length;
    } else { next = TOKEN + (value && !/^\s/.test(value) ? " " : "") + value; position = TOKEN.length; }
    onChange(next); display(next, position);
  };
  return <><div key={revision} ref={ref} className="in b5-tok" id="f-title" role="textbox" aria-label="订阅名" contentEditable={!readOnly} suppressContentEditableWarning spellCheck={false}
    onKeyDown={event => { if (event.key === "Enter") event.preventDefault(); }}
    onPaste={event => { event.preventDefault(); document.execCommand("insertText", false, event.clipboardData.getData("text/plain").replace(/\s*\n\s*/g, " ")); }}
    onInput={event => { const text = titleValue(event.currentTarget); onChange(text); if (event.currentTarget.textContent?.includes(TOKEN)) display(text, text.length); }}>
    {rendered.split(TOKEN).map((part, index) => <Fragment key={index}>{index > 0 && <span className="tk" contentEditable={false} data-tok="USERNAME">用户名</span>}{part}</Fragment>)}
  </div><div className="r-tok" id="title-tok">{!readOnly && !value.includes(TOKEN) && <button type="button" className="tk tk-add" data-act="b5-tok" onMouseDown={event => event.preventDefault()} onClick={insertToken}><Icon name="plus" />用户名</button>}</div></>;
}
