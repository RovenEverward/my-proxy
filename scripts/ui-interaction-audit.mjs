#!/usr/bin/env node
// 用已有 e2e 路径巡检；API 人为延迟 150–300ms，报告只保留接口路径与耗时。
import { spawnSync } from "node:child_process";
import { readFileSync, writeFileSync, mkdirSync } from "node:fs";
import path from "node:path";
import os from "node:os";
import { fileURLToPath } from "node:url";
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const out = process.env.MPX_INTERACTION_AUDIT || path.join(os.homedir(), 'mpx-e2e-logs/interaction-audit/before.jsonl');
const specs = ['rules','users','users-online','nodes','inbounds','plans','settings','sub','doctor','nav','start','update','profiles'];
if (!process.argv.includes('--report-only')) {
  mkdirSync(path.dirname(out), { recursive: true });
  writeFileSync(out, '');
  const run = spawnSync('npx', ['playwright','test','-c','internal/ui/e2e',...specs.map(s => s+'.spec.mjs'),'--reporter=line'], { cwd: root, env: { ...process.env, MPX_INTERACTION_AUDIT: out }, stdio: 'inherit' });
  if (run.status) console.error('部分操作路径未走完，报告只包含实际执行记录。');
}
const latest = new Map();
for (const line of readFileSync(out, 'utf8').trim().split('\n').filter(Boolean)) { const r=JSON.parse(line);latest.set(r.id,r); }
const grouped = new Map();
for (const row of latest.values()) {
  if (!row.control || row.tag==='INPUT'&&row.event==='click' || row.tag==='SELECT'&&row.event==='click') continue;
  const key = [row.page,row.control,row.event].join(' / ');
  if (!grouped.has(key)) grouped.set(key,[]);
  grouped.get(key).push(row);
}
const lines=['# 界面交互延迟巡检','','API 人为延迟 150–300ms。首次反馈按可见 DOM 状态变化或原生控件值变化计；最终结果按最后 DOM 变化及关联请求结束计。跨操作重叠与页面关闭的未完成请求不作为独立耗时结论。','','| 页面 / 交互 | 首次反馈 ms | 最终结果 ms | API 链（起点、耗时 ms） | 分类 |','|---|---:|---:|---|---|'];
const fmt=x=>x==null?'未观测':x.toFixed(1);
for (const [key,rows] of [...grouped].sort(([a],[b])=>a.localeCompare(b))) {
  const isolated=rows.filter(r=>!r.overlapped&&r.requests.every(q=>q.end));
  const candidates=isolated.length?isolated:rows;
  const row=candidates.sort((a,b)=>(b.feedback??Infinity)-(a.feedback??Infinity))[0];
  const writes=row.requests.filter(q=>q.method!=='GET');
  const reads=row.requests.filter(q=>q.method==='GET');
  const category=writes.some(w=>w.end&&reads.some(r=>r.start>=w.end))?'C 写后重新读取':row.feedback===null?'D 未观测到 DOM 反馈（需核对是否无操作）':row.feedback>50&&row.requests.length?'B 等待请求':row.feedback>50?'D 本地延迟':'— 即时反馈';
  const seen=new Set(); let repeats=0;
  for(const q of row.requests) {if(q.signature){const key=q.method+' '+q.path+' '+q.signature;if(seen.has(key))repeats++;seen.add(key)}}
  const serial=row.requests.some(q=>row.requests.some(p=>p!==q&&p.end&&q.start>=p.end));
  const note=(serial?'；有串行等待':'；并行/无请求')+(row.requests.some(q=>q.signature)?`；同参重复 ${repeats}`:'；旧记录无参数签名，不判重复');
  const requests=(row.requests.map(q=>`${q.method} ${q.path}（+${fmt(q.start)}，${fmt(q.duration)}）`).join('<br>')||'无')+note;
  lines.push(`| ${key.replaceAll('|','/')} | ${fmt(row.feedback)} | ${fmt(row.final)}${isolated.length?'':'（重叠/未完成）'} | ${requests} | ${category} |`);
}
const report=out.replace(/\.jsonl$/,'.md');writeFileSync(report,lines.join('\n')+'\n');
console.log(`已记录 ${latest.size} 次交互，汇总 ${grouped.size} 项：${report}`);
