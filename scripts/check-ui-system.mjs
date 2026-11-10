#!/usr/bin/env node
// Check the design system without a browser or network connection.
import { readFileSync, readdirSync, existsSync } from "node:fs";
import path from "node:path";
import vm from "node:vm";
import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const assets = path.join(root, "internal/ui/assets");
const source = relative => readFileSync(path.join(root, relative), "utf8");
function files(directory) {
  return readdirSync(directory, { withFileTypes: true }).flatMap(entry => {
    const target = path.join(directory, entry.name);
    return entry.isDirectory() ? files(target) : [target];
  });
}
const tokenPath = path.join(assets, "styles/tokens.css");
const tokens = readFileSync(tokenPath, "utf8");
const declared = new Set([...tokens.matchAll(/(--ui-[\w-]+)\s*:/g)].map(match => match[1]));
assert(declared.size > 40, "The token authority must define the shared roles.");
for (const file of files(assets).filter(file => file.endsWith(".css"))) {
  const css = readFileSync(file, "utf8");
  if (file !== tokenPath) assert(!/--(?:ui-[\w-]+|base|canvas|panel|stone|ink2?|muted|faint|sans|mono)\s*:/.test(css), `${file}: token declarations belong in styles/tokens.css`);
  for (const match of css.matchAll(/var\((--ui-[\w-]+)/g)) assert(declared.has(match[1]), `${file}: undefined token ${match[1]}`);
  if (file !== tokenPath) {
    assert(!/font-size:\s*[\d.]+px/.test(css), `${file}: font sizes must use semantic type roles`);
    for (const match of css.matchAll(/\bfont:([^;{}]+)/g)) assert(!/^\s*(?:(?:normal|italic|oblique|small-caps|bold|bolder|lighter|[1-9]00)\s+)*[\d.]+px\b/.test(match[1]), `${file}: font shorthand must use a semantic size`);
  }
}
assert(source("internal/ui/assets/app.css").includes('@import url("/styles/tokens.css")'), "Product CSS must load the token authority.");
assert(!existsSync(path.join(assets, "styles/foundation.css")), "Remove the replaced token source.");
assert(!existsSync(path.join(assets, "styles/terminus/token.css")), "Remove the replaced palette source.");

const sandbox = vm.createContext({});
vm.runInContext(source("internal/ui/assets/icons.js") + "\n" + source("internal/ui/assets/components.js") + "\nglobalThis.system = { ICONS, H, val, ic };", sandbox);
const { ICONS, H, val, ic } = sandbox.system;
assert(Object.isFrozen(ICONS), "The icon registry must be immutable.");
const payload = '<img src=x onerror="alert(1)">';
assert.equal(val(H`<p>${payload}</p>`), '<p>&lt;img src=x onerror=&quot;alert(1)&quot;&gt;</p>');
assert.throws(() => ic("not-an-icon"), /Unknown UI icon/);
for (const file of files(assets).filter(file => file.endsWith(".js"))) {
  const js = readFileSync(file, "utf8");
  if (path.basename(file) !== "icons.js") assert(!/<(?:path|circle|rect|polyline|polygon|ellipse|line)\b/.test(js), `${file}: use an official icon through ic()`);
  assert(!/Object\.assign\(ICONS/.test(js), `${file}: the shared icon registry is immutable`);
  for (const match of js.matchAll(/\bic\(["']([^"']+)["']/g)) assert(Object.hasOwn(ICONS, match[1]), `${file}: unknown icon ${match[1]}`);
}
const index = source("internal/ui/assets/index.html");
assert(index.indexOf('src="/icons.js"') < index.indexOf('src="/components.js"'), "Load icons before components.");
assert(index.indexOf('src="/components.js"') < index.indexOf('src="/app.js"'), "Load components before application startup.");
const catalog = source("internal/ui/assets/components.html");
assert(catalog.includes('src="/components.js"') && !catalog.includes('src="/app.js"'), "The catalog must reuse primitives without starting the application.");
for (const relative of ["internal/ui/assets/index.html", "internal/ui/assets/components.html"]) {
  assert(!/<script\s*>|\son(?:click|change|input)=/i.test(source(relative)), `${relative}: use CSP-compatible external scripts`);
}
for (const relative of ["internal/ui/ux/README.md", "internal/ui/ux/patterns.md", "internal/ui/ux/components.md", "internal/ui/ux/evaluation.md"]) {
  const text = source(relative);
  for (const match of text.matchAll(/\]\(([^)]+)\)/g)) {
    const target = match[1].split("#")[0];
    if (!target || /^(?:https?:|mailto:)/.test(target)) continue;
    assert(existsSync(path.resolve(path.dirname(path.join(root, relative)), decodeURI(target))), `${relative}: broken link ${target}`);
  }
}
console.log(`UI architecture passed: ${declared.size} semantic token roles, ${Object.keys(ICONS).length} official icon aliases, shared primitives, CSP, and four linked UX layers.`);
