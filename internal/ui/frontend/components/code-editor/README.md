# Code editor

The editor preserves the existing textarea overlay and syntax classes. The parent owns text, validation, and document loading.

```tsx
import { useRef, useState } from "react";
import { CodeEditor, type CodeEditorHandle } from "./components/code-editor";

const editor = useRef<CodeEditorHandle>(null);
const [value, setValue] = useState('{"enabled":true}');

// Existing #tpl-ed layout rules size the root .ed element.
<CodeEditor ref={editor} id="tpl-ed" value={value} language="json"
  onChange={setValue} onCursorChange={(line, column) => updatePosition(line, column)} />;

// Formatting and loading replace the controlled value. Each replacement is undoable.
setValue(JSON.stringify(JSON.parse(value), null, 2) + "\n");
setValue(await file.text());

// Validation stays with the parent.
editor.current?.mark(12);
editor.current?.focus();
```

## Shape and choice

Two structures were considered:

1. Native textarea history, with browser insertion commands for Tab. Parent replacements bypass browser history. This fails the controlled replacement requirement.
2. Controlled text, with an explicit history of accepted text and selection snapshots. Keyboard and native input history commands use this history.

The second structure owns all undo decisions. It uses three modules: the component, a plain TypeScript history helper, and a React syntax renderer. The renderer returns text and React nodes. The history helper contains no DOM access. The component owns DOM selection, scroll synchronization, and controlled value reconciliation.

Only accepted controlled values enter history. Consecutive text insertion and deletion form short groups. Paste, Tab, parent replacement, and navigation between history entries create boundaries. Each composition session forms one group. Undo and redo restore selection and scroll. A new edit after undo discards the redo branch. History retains 100 entries.

We accept full text snapshots to keep parent replacements and cursor restoration consistent. We accept lexical highlighting because validation belongs to the caller.

## Contract

`CodeEditor` forwards a `CodeEditorHandle` with `focus(): void` and `mark(line: number, jump?: boolean): void`.

Props: `id?: string`, `value: string`, `language: 'json' | 'yaml'`, `readOnly?: boolean`, `errorLine?: number`, `onChange(value: string): void`, and `onCursorChange?(line: number, column: number): void`.

`id` belongs to the root `.ed` element. When present, the textarea uses `${id}-input`. Labels can target that derived ID. The textarea also has the existing Chinese accessible name. Line and column numbers start at one. Columns use textarea UTF-16 offsets. `mark(0)` clears an imperative mark. `mark(line)` selects the line start and scrolls to it without moving focus. `mark(line, false)` only highlights. Changing text or `errorLine` clears the imperative override. The parent can set `errorLine` without moving the cursor.

The parent must size the root through its existing layout. The template drawer uses `id="tpl-ed"` for its flex sizing. Keep the same component mounted to retain history through format, load, and revert. Use a React `key` when a different document needs separate history.

The highlighter recognizes JSON strings, keys, numbers, booleans, and null. YAML adds comments, block scalars, quoted strings, mapping keys, and common scalar values. This is not a parser. It does not validate templates.

Tab replaces the selection with two spaces. Shift+Tab retains native focus navigation. Ctrl/Cmd+Z undoes. Ctrl/Cmd+Shift+Z and Ctrl+Y redo. Native `beforeinput` history commands use the same history when the browser emits them.
