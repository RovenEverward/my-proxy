#!/usr/bin/env node
// Check the design system without a browser or network connection.
// Source of truth: internal/ui/frontend (React + TypeScript + Vite + shadcn/ui).
// Generated files (assets/build, assets/index.html, assets/components.html,
// assets/boot.js) are packaged artifacts; Go tests cover their reachability.
import { readFileSync, readdirSync, existsSync } from "node:fs";
import path from "node:path";
import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const assets = path.join(root, "internal/ui/assets");
const frontend = path.join(root, "internal/ui/frontend");
const source = relative => readFileSync(path.join(root, relative), "utf8");
function files(directory) {
  if (!existsSync(directory)) return [];
  return readdirSync(directory, { withFileTypes: true }).flatMap(entry => {
    const target = path.join(directory, entry.name);
    return entry.isDirectory() ? files(target) : [target];
  });
}

/* ---------- Tokens ---------- */

const tokenPath = path.join(assets, "styles/tokens.css");
const tokens = readFileSync(tokenPath, "utf8");
const declared = new Set([...tokens.matchAll(/(--ui-[\w-]+)\s*:/g)].map(match => match[1]));
assert(declared.size > 40, "The token authority must define the shared roles.");
const cssFiles = [...files(frontend).filter(file => file.endsWith(".css")), ...files(assets).filter(file => file.endsWith(".css") && !file.startsWith(path.join(assets, "build")))];
assert(cssFiles.length > 5, "Expected product CSS sources under frontend/ and assets/.");
for (const file of cssFiles) {
  const css = readFileSync(file, "utf8");
  if (file !== tokenPath) assert(!/--(?:ui-[\w-]+|base|canvas|panel|stone|ink2?|muted|faint|sans|mono)\s*:/.test(css), `${file}: token declarations belong in styles/tokens.css`);
  for (const match of css.matchAll(/var\((--ui-[\w-]+)/g)) assert(declared.has(match[1]), `${file}: undefined token ${match[1]}`);
  if (file !== tokenPath) {
    assert(!/font-size:\s*[\d.]+px/.test(css), `${file}: font sizes must use semantic type roles`);
    for (const match of css.matchAll(/\bfont:([^;{}]+)/g)) assert(!/^\s*(?:(?:normal|italic|oblique|small-caps|bold|bolder|lighter|[1-9]00)\s+)*[\d.]+px\b/.test(match[1]), `${file}: font shorthand must use a semantic size`);
  }
}
assert(source("internal/ui/frontend/styles/index.css").includes('@import "../../assets/styles/tokens.css"'), "The React style entry must load the token authority first.");
assert(!existsSync(path.join(assets, "styles/foundation.css")), "Remove the replaced token source.");
assert(!existsSync(path.join(assets, "styles/terminus/token.css")), "Remove the replaced palette source.");

/* ---------- Icons ---------- */

const iconMap = source("internal/ui/frontend/components/icon-map.ts");
assert(iconMap.includes('from "lucide-react"'), "icon-map.ts must import official lucide-react components.");
assert(/Object\.freeze/.test(iconMap), "The icon registry must be immutable.");
const aliases = new Set([...iconMap.matchAll(/"([a-z][a-z0-9]*)": "[a-z][a-z0-9-]*"/g)].map(match => match[1]));
assert(aliases.size > 40, "The icon registry must cover the shared aliases.");
const iconsConfig = JSON.parse(source("internal/ui/icons.json"));

/* ---------- Stack and sources ---------- */

const pkg = JSON.parse(source("package.json"));
for (const name of ["react", "react-dom", "lucide-react"]) assert(pkg.dependencies?.[name], `package.json must declare ${name}.`);
assert.equal(pkg.dependencies["lucide-react"].replace(/^\^/, ""), iconsConfig.source.version, "lucide-react must pin the icons.json version.");
const lock = JSON.parse(source("package-lock.json"));
assert.equal(lock.packages?.["node_modules/lucide-react"]?.version, iconsConfig.source.version, "lucide-react must resolve to the icons.json pinned version.");
const tsconfig = JSON.parse(source("internal/ui/frontend/tsconfig.json"));
assert.equal(tsconfig.compilerOptions?.strict, true, "The frontend must compile under strict TypeScript.");
assert.equal(tsconfig.compilerOptions?.jsx, "react-jsx", "The frontend must use the React JSX transform.");
const shadcn = JSON.parse(source("components.json"));
assert.equal(shadcn.iconLibrary, "lucide", "shadcn components must use the official Lucide library.");
assert.equal(shadcn.tsx, true, "shadcn components must be TypeScript.");

const codeFiles = files(frontend).filter(file => /\.(?:tsx?|mts)$/.test(file) && file !== path.join(frontend, "vite.config.ts"));
assert(codeFiles.length > 10, "Expected the React sources under frontend/.");
for (const file of codeFiles) {
  const rel = path.relative(frontend, file).replaceAll(path.sep, "/");
  const text = readFileSync(file, "utf8");
  // React owns escaping; legacy helpers and raw HTML injection are gone.
  assert(!/dangerouslySetInnerHTML/.test(text), `${rel}: use React nodes, not raw HTML injection`);
  assert(!/(?:\.innerHTML|\.outerHTML)\s*=[^=]/.test(text), `${rel}: use React nodes, not innerHTML assignment`);
  assert(!/insertAdjacentHTML|document\.write\s*\(/.test(text), `${rel}: use React nodes, not HTML string injection`);
  assert(!/\bH\s*`/.test(text), `${rel}: the legacy H tagged template is gone; return JSX`);
  // Icons: only official lucide-react, vendored shadcn wrappers, or the shared Icon primitive.
  assert(!/<\/?(?:path|circle|rect|polyline|polygon|ellipse|line)(?=[\s/>])/.test(text), `${rel}: no hand-drawn SVG geometry; use the Icon primitive`);
  assert(!/createElement\(\s*["']svg/.test(text), `${rel}: no hand-drawn SVG elements; use the Icon primitive`);
  const lucideImport = /import\s+(?:type\s+)?(?:\{[^}]*\}|\*\s+as\s+\w+|\w+)\s+from\s+["']lucide-react["']/.test(text);
  if (lucideImport) assert(/^components\/(?:icon-map\.ts|ui\/)/.test(rel), `${rel}: product code uses the Icon primitive; direct lucide-react is reserved for icon-map.ts and shadcn ui wrappers`);
  // Every literal icon alias must exist in the registry. shadcn ui wrappers use
  // `icon:` as a size variant, so that pattern only scans product code.
  for (const match of text.matchAll(/Icon\s+name=["']([a-z][a-z0-9]*)["']/g)) assert(aliases.has(match[1]), `${rel}: unknown icon ${match[1]}`);
  if (!rel.startsWith("components/ui/")) for (const match of text.matchAll(/\bicon:\s*["']([a-z][a-z0-9-]*)["']/g)) assert(aliases.has(match[1]), `${rel}: unknown icon ${match[1]}`);
}

/* ---------- Entries ---------- */

for (const relative of ["internal/ui/frontend/index.html", "internal/ui/frontend/components.html"]) {
  const html = source(relative);
  assert(!/<script\s*>|\son(?:click|change|input)=/i.test(html), `${relative}: use CSP-compatible external scripts only`);
  assert(html.includes('<div id="root"></div>'), `${relative}: mount the React root`);
  assert(!/<(?:img|video|canvas|form|button)\b/.test(html), `${relative}: the shell stays static; UI lives in React`);
}
const index = source("internal/ui/frontend/index.html");
assert(index.includes('src="/boot.js"'), "index.html loads the compiled bootstrap.");
assert(index.includes('src="/main.tsx"'), "index.html loads the React application entry.");
const catalogHtml = source("internal/ui/frontend/components.html");
assert(catalogHtml.includes('src="/catalog.tsx"'), "components.html loads the catalog entry.");
assert(!catalogHtml.includes('src="/main.tsx"'), "The catalog must not start the application.");
const main = source("internal/ui/frontend/main.tsx");
assert(main.includes('<App'), "main.tsx mounts the application shell.");
assert(main.includes('import "./styles/index.css"'), "main.tsx loads the shared stylesheet.");
const catalogEntry = source("internal/ui/frontend/catalog.tsx");
assert(catalogEntry.includes('from "./pages/catalog"') && catalogEntry.includes("<CatalogPage"), "The catalog entry mounts its independent page.");
const catalog = source("internal/ui/frontend/pages/catalog/index.tsx");
assert(catalog.includes('from "@/components/primitives"'), "The catalog reuses the real primitives.");
assert(catalog.includes('from "@/components/ui/button"'), "The catalog reuses the shadcn button.");
assert(!/from\s+["'][^"']*app\/App/.test(catalogEntry + catalog), "The catalog must not import the application shell.");
assert(existsSync(path.join(frontend, "boot.ts")), "boot.ts is the source of the compiled /boot.js.");

/* ---------- Documentation layers ---------- */

for (const relative of ["internal/ui/ux/README.md", "internal/ui/ux/patterns.md", "internal/ui/ux/components.md", "internal/ui/ux/evaluation.md"]) {
  const text = source(relative);
  for (const match of text.matchAll(/\]\(([^)]+)\)/g)) {
    const target = match[1].split("#")[0];
    if (!target || /^(?:https?:|mailto:)/.test(target)) continue;
    assert(existsSync(path.resolve(path.dirname(path.join(root, relative)), decodeURI(target))), `${relative}: broken link ${target}`);
  }
}
console.log(`UI architecture passed: ${declared.size} semantic token roles, ${aliases.size} official icon aliases, React sources clean, CSP entries, and four linked UX layers.`);
