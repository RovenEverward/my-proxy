// 更新说明用的小号 Markdown 渲染器:先整体转义,再按行拼 HTML。
// 支持 # / ## / ### 标题、- / * / 1. 列表(缩进一级)、**粗体**、`行内代码`、
// ``` 代码块、[文字](https://…) 链接(只认 http/https)与段落换行。
// 没有外部依赖;所有原文先经过 esc,拼出来的标签只有这里写死的几种,
// 不存在把原文当 HTML 插进去的路径。
"use strict";
(() => {
  const esc = v => String(v ?? "").replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

  // inline 处理一行(已转义)里的行内标记。先按反引号切开,代码段原样保留,
  // 不再解析粗体和链接。
  function inline(s) {
    return s.split(/(`[^`]*`)/g).map(part => {
      if (/^`[^`]*`$/.test(part)) return `<code>${part.slice(1, -1)}</code>`;
      let t = part.replace(/\[([^\]]+)\]\(([^)\s]+)\)/g, (m, text, href) => {
        // href 已转义:引号、尖括号都成了实体,不会跳出属性;再限定协议。
        if (!/^https?:\/\//i.test(href)) return m;
        return `<a href="${href}" target="_blank" rel="noopener">${text}</a>`;
      });
      t = t.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
      return t;
    }).join("");
  }

  // 列表:按缩进(2 个空格或 1 个 tab 算一级)最多撑到两级,超出的一律并进当前级。
  function renderList(items) {
    let html = "", stack = [];
    const tag = o => o ? "ol" : "ul";
    const close = () => { const top = stack.pop(); html += `</li></${tag(top.ordered)}>`; };
    for (const it of items) {
      const indent = it.indent >= 2 ? 1 : 0;
      while (stack.length && indent < stack[stack.length - 1].indent) close();
      if (!stack.length || indent > stack[stack.length - 1].indent) {
        stack.push({ ordered: it.ordered, indent });
        html += `<${tag(it.ordered)}><li>${inline(it.text)}`;
      } else if (stack[stack.length - 1].ordered !== it.ordered) {
        close();
        stack.push({ ordered: it.ordered, indent });
        html += `<${tag(it.ordered)}><li>${inline(it.text)}`;
      } else {
        html += `</li><li>${inline(it.text)}`;
      }
    }
    while (stack.length) close();
    return html;
  }

  function renderMarkdown(src) {
    const lines = esc(src).split(/\r?\n/);
    const out = [];
    let i = 0;
    while (i < lines.length) {
      const line = lines[i];
      // 代码块:``` 到下一个 ```(没闭合就到文末)。
      const fence = /^\s*```/.exec(line);
      if (fence) {
        const body = [];
        i++;
        while (i < lines.length && !/^\s*```/.test(lines[i])) body.push(lines[i++]);
        if (i < lines.length) i++; // 跳过收尾的 ```
        out.push(`<pre><code>${body.join("\n")}</code></pre>`);
        continue;
      }
      if (!line.trim()) { i++; continue; }
      const head = /^(#{1,3})\s+(.*)$/.exec(line);
      if (head) {
        const n = head[1].length;
        out.push(`<h${n}>${inline(head[2].trim())}</h${n}>`);
        i++;
        continue;
      }
      if (/^\s*(?:[-*]|\d+\.)\s+/.test(line)) {
        const items = [];
        while (i < lines.length) {
          const m = /^(\s*)(?:([-*])|(\d+)\.)\s+(.*)$/.exec(lines[i]);
          if (!m) break;
          items.push({ indent: m[1].replace(/\t/g, "  ").length, ordered: !!m[3], text: m[4] });
          i++;
        }
        out.push(renderList(items));
        continue;
      }
      // 普通段落:连续非空、非标题、非列表、非代码围栏的行用 <br> 连接。
      const para = [];
      while (i < lines.length && lines[i].trim() && !/^\s*```/.test(lines[i]) && !/^(#{1,3})\s+/.test(lines[i]) && !/^\s*(?:[-*]|\d+\.)\s+/.test(lines[i])) {
        para.push(lines[i++]);
      }
      out.push(`<p>${para.map(inline).join("<br>")}</p>`);
    }
    return out.join("");
  }

  window.renderMarkdown = renderMarkdown;
})();
