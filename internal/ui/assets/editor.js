// 轻量代码编辑器:textarea 叠在高亮层上,左边行号,底部状态栏。不依赖任何外部库。
// 高亮只认 JSON 与 YAML 的几类记号(键、字符串、数字、布尔/null、注释),够看清结构即可。
"use strict";

const hlEsc = s => s.replace(/[&<>]/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;" }[c]));
function hlJSONLine(line) {
  return line.replace(/("(?:[^"\\]|\\.)*")(\s*:)?|\b(true|false|null)\b|(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)|[&<>]/g, (m, str, colon, kw, num) => {
    if (str) return colon ? `<i class="t-k">${hlEsc(str)}</i>${colon}` : `<i class="t-s">${hlEsc(str)}</i>`;
    if (kw) return `<i class="t-b">${kw}</i>`;
    if (num) return `<i class="t-n">${num}</i>`;
    return hlEsc(m);
  });
}
function hlYAMLLine(line) {
  const ci = (() => { let q = null; for (let i = 0; i < line.length; i++) { const c = line[i]; if (q) { if (c === q) q = null; } else if (c === '"' || c === "'") q = c; else if (c === "#" && (i === 0 || /\s/.test(line[i - 1]))) return i; } return -1; })();
  const code = ci >= 0 ? line.slice(0, ci) : line, cm = ci >= 0 ? `<i class="t-c">${hlEsc(line.slice(ci))}</i>` : "";
  const m = /^(\s*(?:-\s+)?)([^\s#'"][^:#]*?|"[^"]*"|'[^']*')(:)(\s|$)(.*)$/.exec(code);
  const val = v => v.replace(/("(?:[^"\\]|\\.)*"|'[^']*')|\b(true|false|null|~)\b|(^|[\s,[])(-?\d+(?:\.\d+)?)(?=$|[\s,\]])|[&<>]/g, (x, s, kw, pre, num) => s ? `<i class="t-s">${hlEsc(s)}</i>` : kw ? `<i class="t-b">${kw}</i>` : num ? `${pre}<i class="t-n">${num}</i>` : hlEsc(x));
  if (m) return `${hlEsc(m[1])}<i class="t-k">${hlEsc(m[2])}</i>${m[3]}${m[4]}${val(m[5])}${cm}`;
  const d = /^(\s*-\s?)(.*)$/.exec(code);
  return (d ? hlEsc(d[1]) + val(d[2]) : val(code)) + cm;
}

// codeEditor(el, { text, lang, readOnly, onChange }) → { get value, set value, mark(line, msg), focus() }
function codeEditor(host, { text, lang, readOnly, onChange }) {
  host.classList.add("ed");
  host.innerHTML = `<div class="ed-g" aria-hidden="true"></div><div class="ed-m"><pre class="ed-h" aria-hidden="true"></pre><textarea class="ed-t" spellcheck="false" autocapitalize="off" autocomplete="off" wrap="off" aria-label="模板正文"></textarea></div>`;
  const g = host.querySelector(".ed-g"), pre = host.querySelector(".ed-h"), ta = host.querySelector(".ed-t");
  ta.value = text; ta.readOnly = !!readOnly; ta.setSelectionRange(0, 0);
  let bad = 0, raf = 0;
  const hl = lang === "yaml" ? hlYAMLLine : hlJSONLine;
  const paint = () => {
    raf = 0;
    const lines = ta.value.split("\n");
    pre.innerHTML = lines.map((l, i) => `<span class="ln${i + 1 === bad ? " bad" : ""}">${hl(l) || " "}</span>`).join("\n") + "\n";
    let gh = ""; for (let i = 1; i <= lines.length; i++) gh += `<span${i === bad ? ' class="bad"' : ""}>${i}</span>`;
    g.innerHTML = gh;
    sync();
  };
  const sync = () => { pre.style.transform = `translate(${-ta.scrollLeft}px,${-ta.scrollTop}px)`; g.style.transform = `translateY(${-ta.scrollTop}px)`; };
  ta.addEventListener("input", () => { bad = 0; if (!raf) raf = requestAnimationFrame(paint); onChange?.(); });
  ta.addEventListener("scroll", sync);
  ta.addEventListener("keydown", e => {
    if (e.key === "Tab" && !ta.readOnly) { e.preventDefault(); document.execCommand("insertText", false, "  "); }
  });
  for (const ev of ["keyup", "click", "select"]) ta.addEventListener(ev, () => onChange?.("cursor"));
  paint();
  return {
    ta,
    get value() { return ta.value; },
    set value(v) { ta.value = v; bad = 0; paint(); onChange?.(); },
    cursor() { const before = ta.value.slice(0, ta.selectionStart).split("\n"); return [before.length, before[before.length - 1].length + 1]; },
    // 标出出错的那一行并滚过去。
    mark(line, jump = true) {
      bad = line || 0; paint();
      if (line && jump) { const lh = parseFloat(getComputedStyle(ta).lineHeight) || 19; ta.scrollTop = Math.max(0, (line - 4) * lh); const pos = ta.value.split("\n").slice(0, line - 1).join("\n").length + (line > 1 ? 1 : 0); ta.setSelectionRange(pos, pos); sync(); }
    },
    focus() { ta.focus(); },
  };
}
