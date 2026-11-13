import {
  Fragment, forwardRef, useCallback, useImperativeHandle, useLayoutEffect, useMemo, useReducer, useRef, useState,
  type ChangeEvent, type KeyboardEvent,
} from "react";
import { highlightLines, type EditorLanguage } from "./highlight";
import { clampSnapshot, EditorHistory, type EditKind, type EditorSnapshot } from "./history";

export interface CodeEditorProps {
  id?: string;
  value: string;
  language: EditorLanguage;
  readOnly?: boolean;
  errorLine?: number;
  onChange(value: string): void;
  onCursorChange?(line: number, column: number): void;
}

export interface CodeEditorHandle {
  focus(): void;
  mark(line: number, jump?: boolean): void;
}

interface PendingEdit {
  snapshot: EditorSnapshot;
  kind: EditKind;
  time: number;
  restoreSelection: boolean;
  direction?: -1 | 1;
}

function snapshot(textarea: HTMLTextAreaElement): EditorSnapshot {
  return {
    value: textarea.value,
    start: textarea.selectionStart,
    end: textarea.selectionEnd,
    direction: textarea.selectionDirection,
    scrollTop: textarea.scrollTop,
    scrollLeft: textarea.scrollLeft,
  };
}

function restore(textarea: HTMLTextAreaElement, state: EditorSnapshot): void {
  textarea.setSelectionRange(state.start, state.end, state.direction);
  textarea.scrollTop = state.scrollTop;
  textarea.scrollLeft = state.scrollLeft;
}

function editKind(inputType: string, composing: boolean): EditKind {
  if (composing || inputType === "insertCompositionText") return "composition";
  return inputType === "insertText" || inputType === "deleteContentBackward" || inputType === "deleteContentForward"
    ? inputType : "atomic";
}

function validLine(line: number | undefined, count: number): number {
  return line !== undefined && Number.isInteger(line) && line > 0 && line <= count ? line : 0;
}

export const CodeEditor = forwardRef<CodeEditorHandle, CodeEditorProps>(function CodeEditor(
  { id, value, language, readOnly = false, errorLine, onChange, onCursorChange }, forwardedRef,
) {
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const highlightRef = useRef<HTMLPreElement>(null);
  const gutterRef = useRef<HTMLDivElement>(null);
  const historyRef = useRef<EditorHistory>();
  if (!historyRef.current) {
    historyRef.current = new EditorHistory({ value, start: 0, end: 0, direction: "none", scrollTop: 0, scrollLeft: 0 });
  }
  const history = historyRef.current;
  const pendingRef = useRef<PendingEdit>();
  const composingRef = useRef(false);
  const cursorRef = useRef<{ line: number; column: number }>();
  const [, render] = useReducer((revision: number) => revision + 1, 0);
  const [manualMark, setManualMark] = useState<number>();
  const lines = useMemo(() => highlightLines(value, language), [value, language]);
  const badLine = validLine(manualMark ?? errorLine, lines.length);

  const reportCursor = useCallback(() => {
    const textarea = textareaRef.current;
    if (!textarea) return;
    const before = textarea.value.slice(0, textarea.selectionStart);
    const line = before.split("\n").length;
    const column = before.length - before.lastIndexOf("\n");
    if (cursorRef.current?.line === line && cursorRef.current.column === column) return;
    cursorRef.current = { line, column };
    onCursorChange?.(line, column);
  }, [onCursorChange]);

  const syncScroll = useCallback(() => {
    const textarea = textareaRef.current;
    if (!textarea) return;
    if (highlightRef.current) {
      highlightRef.current.style.transform = `translate(${-textarea.scrollLeft}px, ${-textarea.scrollTop}px)`;
    }
    if (gutterRef.current) gutterRef.current.style.transform = `translateY(${-textarea.scrollTop}px)`;
    history.capture(snapshot(textarea), composingRef.current);
  }, [history]);

  const captureSelection = useCallback(() => {
    const textarea = textareaRef.current;
    if (textarea) history.capture(snapshot(textarea), composingRef.current);
    reportCursor();
  }, [history, reportCursor]);

  const requestEdit = useCallback((state: EditorSnapshot, kind: EditKind, restoreSelection: boolean, direction?: -1 | 1) => {
    pendingRef.current = { snapshot: state, kind, time: Date.now(), restoreSelection, direction };
    onChange(state.value);
    render();
  }, [onChange]);

  const moveHistory = useCallback((direction: -1 | 1) => {
    if (readOnly || composingRef.current) return;
    const textarea = textareaRef.current;
    if (textarea) history.capture(snapshot(textarea));
    history.breakGroup();
    const next = history.peek(direction);
    if (next) requestEdit(next, "atomic", true, direction);
  }, [history, readOnly, requestEdit]);

  useLayoutEffect(() => {
    const textarea = textareaRef.current;
    if (!textarea) return;
    const pending = pendingRef.current;
    pendingRef.current = undefined;
    if (pending && value === pending.snapshot.value) {
      if (pending.direction) history.move(pending.direction);
      else history.record(pending.snapshot, pending.kind, pending.time);
      if (pending.restoreSelection && !composingRef.current) restore(textarea, history.current);
    } else if (value !== history.current.value) {
      const next = clampSnapshot(history.current, value);
      history.record(next);
      if (!composingRef.current) restore(textarea, next);
    } else if (pending && !composingRef.current) {
      // The parent kept the previous controlled value.
      restore(textarea, history.current);
    }
    syncScroll();
    reportCursor();
  });

  useLayoutEffect(() => {
    setManualMark(undefined);
  }, [value, errorLine]);

  useLayoutEffect(() => {
    const textarea = textareaRef.current;
    if (!textarea) return;
    const beforeInput = (event: InputEvent) => {
      if (event.inputType === "historyUndo" || event.inputType === "historyRedo") {
        if (!readOnly && event.cancelable) {
          event.preventDefault();
          moveHistory(event.inputType === "historyUndo" ? -1 : 1);
        }
        return;
      }
      history.capture(snapshot(textarea), composingRef.current);
    };
    textarea.addEventListener("beforeinput", beforeInput);
    return () => textarea.removeEventListener("beforeinput", beforeInput);
  }, [history, moveHistory, readOnly]);

  useImperativeHandle(forwardedRef, () => ({
    focus() {
      textareaRef.current?.focus();
      reportCursor();
    },
    mark(line, jump = true) {
      const textarea = textareaRef.current;
      if (!textarea) return;
      const target = validLine(line, textarea.value.split("\n").length);
      setManualMark(target);
      if (!target || !jump) return;
      let offset = 0;
      for (let current = 1; current < target; current += 1) offset = textarea.value.indexOf("\n", offset) + 1;
      textarea.setSelectionRange(offset, offset);
      const lineHeight = Number.parseFloat(getComputedStyle(textarea).lineHeight)
        || highlightRef.current?.querySelector(".ln")?.getBoundingClientRect().height || 19;
      textarea.scrollTop = Math.max(0, (target - 4) * lineHeight);
      textarea.scrollLeft = 0;
      history.breakGroup();
      syncScroll();
      reportCursor();
    },
  }), [history, reportCursor, syncScroll]);

  const handleInput = (event: ChangeEvent<HTMLTextAreaElement>) => {
    if (readOnly) return;
    const nativeEvent = event.nativeEvent;
    const inputType = "inputType" in nativeEvent && typeof nativeEvent.inputType === "string" ? nativeEvent.inputType : "";
    if (inputType === "historyUndo" || inputType === "historyRedo") {
      moveHistory(inputType === "historyUndo" ? -1 : 1);
      return;
    }
    requestEdit(snapshot(event.currentTarget), editKind(inputType, composingRef.current), false);
  };

  const handleKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.nativeEvent.isComposing || composingRef.current) return;
    const key = event.key.toLowerCase();
    if (!readOnly && (event.ctrlKey || event.metaKey) && !event.altKey) {
      if (key === "z" || (key === "y" && !event.shiftKey)) {
        event.preventDefault();
        moveHistory(key === "y" || event.shiftKey ? 1 : -1);
        return;
      }
    }
    if (event.key === "Tab" && !event.shiftKey && !event.ctrlKey && !event.metaKey && !event.altKey && !readOnly) {
      event.preventDefault();
      const before = snapshot(event.currentTarget);
      history.capture(before);
      history.breakGroup();
      requestEdit({
        ...before,
        value: before.value.slice(0, before.start) + "  " + before.value.slice(before.end),
        start: before.start + 2,
        end: before.start + 2,
        direction: "none",
      }, "atomic", true);
      return;
    }
    if (event.key.length > 1 && event.key !== "Backspace" && event.key !== "Delete" && !event.key.endsWith("Shift")) {
      history.breakGroup();
    }
    history.capture(snapshot(event.currentTarget));
  };

  return <div id={id} className="ed">
    <div ref={gutterRef} className="ed-g" aria-hidden="true">
      {lines.map((_, index) => <span key={index} className={index + 1 === badLine ? "bad" : undefined}>{index + 1}</span>)}
    </div>
    <div className="ed-m">
      <pre ref={highlightRef} className="ed-h" aria-hidden="true">
        {lines.map((line, index) => <Fragment key={index}>
          <span className={index + 1 === badLine ? "ln bad" : "ln"}>{line.length ? line : " "}</span>{"\n"}
        </Fragment>)}
      </pre>
      <textarea
        ref={textareaRef}
        id={id ? `${id}-input` : undefined}
        className="ed-t"
        value={value}
        readOnly={readOnly}
        spellCheck={false}
        autoCapitalize="off"
        autoComplete="off"
        autoCorrect="off"
        wrap="off"
        aria-label="模板正文"
        aria-invalid={badLine > 0 || undefined}
        onChange={handleInput}
        onScroll={syncScroll}
        onKeyDown={handleKeyDown}
        onKeyUp={captureSelection}
        onSelect={captureSelection}
        onClick={captureSelection}
        onFocus={captureSelection}
        onPointerDown={() => history.breakGroup()}
        onBlur={() => history.breakGroup()}
        onCompositionStart={() => {
          history.breakGroup();
          composingRef.current = true;
        }}
        onCompositionEnd={() => {
          composingRef.current = false;
          history.breakGroup();
          captureSelection();
        }}
      />
    </div>
  </div>;
});
