import type { ReactNode } from "react";

export type EditorLanguage = "json" | "yaml";
type TokenClass = "t-k" | "t-s" | "t-b" | "t-n" | "t-c";
interface Token { text: string; className?: TokenClass }

function nodes(tokens: Token[]): ReactNode[] {
  return tokens.filter(token => token.text.length > 0).map((token, index) => token.className
    ? <i key={index} className={token.className}>{token.text}</i>
    : token.text);
}

function jsonTokens(line: string): Token[] {
  const tokens: Token[] = [];
  const pattern = /"(?:[^"\\]|\\.)*"|\b(?:true|false|null)\b|-?\b\d+(?:\.\d+)?(?:[eE][+-]?\d+)?/g;
  let cursor = 0;
  for (const match of line.matchAll(pattern)) {
    const index = match.index;
    if (index > cursor) tokens.push({ text: line.slice(cursor, index) });
    const text = match[0];
    const className: TokenClass = text[0] === '"'
      ? (/^\s*:/.test(line.slice(index + text.length)) ? "t-k" : "t-s")
      : /^(?:true|false|null)$/.test(text) ? "t-b" : "t-n";
    tokens.push({ text, className });
    cursor = index + text.length;
  }
  if (cursor < line.length) tokens.push({ text: line.slice(cursor) });
  return tokens;
}

function yamlCommentStart(line: string): number {
  let quote: "'" | '"' | undefined;
  for (let index = 0; index < line.length; index += 1) {
    const character = line[index];
    if (quote) {
      if (quote === '"' && character === "\\") index += 1;
      else if (quote === "'" && character === "'" && line[index + 1] === "'") index += 1;
      else if (character === quote) quote = undefined;
    } else if (character === '"' || character === "'") quote = character;
    else if (character === "#" && (index === 0 || /\s/.test(line[index - 1]))) return index;
  }
  return -1;
}

function yamlValueTokens(value: string): Token[] {
  const tokens: Token[] = [];
  const pattern = /"(?:[^"\\]|\\.)*"|'(?:[^']|'')*'|(?:^|(?<=[\s,[{]))(?:true|false|null|~)(?=$|[\s,\]}])|(?:^|(?<=[\s,[{]))[-+]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][-+]?\d+)?(?=$|[\s,\]}])/gi;
  let cursor = 0;
  for (const match of value.matchAll(pattern)) {
    const index = match.index;
    if (index > cursor) tokens.push({ text: value.slice(cursor, index) });
    const text = match[0];
    const className: TokenClass = text[0] === '"' || text[0] === "'"
      ? (/^\s*:(?:\s|$|[\[\]{},])/.test(value.slice(index + text.length)) ? "t-k" : "t-s")
      : /^(?:true|false|null|~)$/i.test(text) ? "t-b" : "t-n";
    tokens.push({ text, className });
    cursor = index + text.length;
  }
  if (cursor < value.length) tokens.push({ text: value.slice(cursor) });
  return tokens;
}

function yamlTokens(line: string): Token[] {
  const commentStart = yamlCommentStart(line);
  const code = commentStart < 0 ? line : line.slice(0, commentStart);
  const mapping = /^(\s*(?:-\s+)?)("(?:[^"\\]|\\.)*"|'(?:[^']|'')*'|[^\s#'"\[\]{},][^:#]*?)(\s*):(?=\s|$)/.exec(code);
  const tokens: Token[] = mapping
    ? [
      { text: mapping[1] },
      { text: mapping[2], className: "t-k" },
      { text: mapping[3] + ":" },
      ...yamlValueTokens(code.slice(mapping[0].length)),
    ]
    : yamlValueTokens(code);
  if (commentStart >= 0) tokens.push({ text: line.slice(commentStart), className: "t-c" });
  return tokens;
}

/** Render source as React text. No source text becomes markup. */
export function highlightLines(value: string, language: EditorLanguage): ReactNode[][] {
  let blockIndent: number | undefined;
  return value.split("\n").map(line => {
    if (language === "json") return nodes(jsonTokens(line));
    const indent = /^ */.exec(line)?.[0].length ?? 0;
    if (blockIndent !== undefined && (!line.trim() || indent > blockIndent)) {
      return nodes([{ text: line, className: "t-s" }]);
    }
    blockIndent = undefined;
    const code = line.slice(0, yamlCommentStart(line) < 0 ? line.length : yamlCommentStart(line)).trimEnd();
    if (/(?:^|:\s+|-\s+)[|>](?:[1-9][+-]?|[+-][1-9]?)?$/.test(code)) blockIndent = indent;
    return nodes(yamlTokens(line));
  });
}
