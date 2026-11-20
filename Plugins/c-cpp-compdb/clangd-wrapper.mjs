// LSP stdio wrapper: on `initialize`, generate compilation databases for the
// workspace, then proxy every message to the real clangd process.
// The IDE only sees a normal language server; no host/Core change is required.

import { spawn } from 'node:child_process';
import { existsSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { ensureDatabases } from './generate-compdb.mjs';

const CLANGD_FALLBACKS = [
  'C:/Program Files/LLVM/bin/clangd.exe',
  'C:/Program Files (x86)/LLVM/bin/clangd.exe',
  '/opt/homebrew/opt/llvm/bin/clangd',
  '/usr/local/opt/llvm/bin/clangd',
  '/usr/bin/clangd',
];

function findClangd() {
  for (const candidate of CLANGD_FALLBACKS) {
    if (existsSync(candidate)) return candidate;
  }
  return 'clangd'; // rely on PATH
}

function log(message) {
  process.stderr.write(`[lithe-compdb] ${message}\n`);
}

/** Splits an LSP byte stream into complete messages. */
function extractMessages(buf) {
  const messages = [];
  let offset = 0;
  while (true) {
    const headerEnd = buf.indexOf('\r\n\r\n', offset, 'utf8');
    if (headerEnd < 0) break;
    const header = buf.subarray(offset, headerEnd).toString('utf8');
    const match = /^Content-Length:\s*(\d+)\s*$/im.exec(header);
    if (!match) { offset = headerEnd + 4; continue; }
    const length = Number(match[1]);
    const bodyStart = headerEnd + 4;
    if (buf.length < bodyStart + length) break;
    let json = null;
    try {
      json = JSON.parse(buf.subarray(bodyStart, bodyStart + length).toString('utf8'));
    } catch { /* non-JSON messages are still forwarded verbatim */ }
    messages.push({ raw: Buffer.from(buf.subarray(offset, bodyStart + length)), json });
    offset = bodyStart + length;
  }
  return { messages, rest: Buffer.from(buf.subarray(offset)) };
}

function workspaceRootFrom(message) {
  const params = message?.json?.params || {};
  const candidates = [
    params.workspaceFolders?.[0]?.uri,
    params.rootUri,
    params.rootPath,
  ].filter(Boolean);
  for (const value of candidates) {
    try {
      if (String(value).startsWith('file:')) return fileURLToPath(value);
      return String(value);
    } catch { /* try the next candidate */ }
  }
  return process.cwd();
}

let buffer = Buffer.alloc(0);
let child = null;
let started = false;

function start(root, buffered) {
  if (started) return;
  started = true;

  let report = [];
  try {
    report = ensureDatabases(root);
  } catch (error) {
    log(`generation failed: ${error?.message || error}`);
  }
  for (const line of report) log(line);

  child = spawn(findClangd(), [], { stdio: ['pipe', 'pipe', 'pipe'], windowsHide: true });
  child.stdout.on('data', (chunk) => process.stdout.write(chunk));
  child.stderr.on('data', (chunk) => process.stderr.write(chunk));
  child.on('exit', (code, signal) => {
    if (signal) process.kill(process.pid, signal);
    else process.exit(code ?? 0);
  });
  child.on('error', (error) => {
    log(`cannot start clangd: ${error.message}`);
    process.exit(1);
  });

  for (const message of buffered) child.stdin.write(message.raw);
  process.stdin.resume();
}

process.stdin.on('data', (chunk) => {
  if (started) {
    child.stdin.write(chunk);
    return;
  }
  buffer = Buffer.concat([buffer, chunk]);
  const { messages, rest } = extractMessages(buffer);
  buffer = rest;
  const initialize = messages.find((m) => m.json?.method === 'initialize');
  if (!initialize) return;
  process.stdin.pause();
  start(workspaceRootFrom(initialize), messages);
});

process.stdin.on('end', () => {
  if (child) child.stdin.end();
  else process.exit(0);
});

for (const signal of ['SIGINT', 'SIGTERM']) {
  process.on(signal, () => {
    if (child && !child.killed) child.kill(signal);
    else process.exit(0);
  });
}

log('ready (waiting for LSP initialize)');
