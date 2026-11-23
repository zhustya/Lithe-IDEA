// Git Graph Lite backend: read-only git queries exposed as plugin functions.
// Uses the official Lithe plugin SDK (vendored, Apache-2.0 in the Core repo).
import { spawn } from 'node:child_process';
import { existsSync, readdirSync, statSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { createBackend, PluginError } from '../vendor/lithe-plugin-sdk/src/index.mjs';

const FIELD = '\x1f';
const RECORD = '\x1e';

function runGit(repo, args, maxBuffer = 16 * 1024 * 1024) {
  return new Promise((done, fail) => {
    const child = spawn('git', args, { cwd: repo, windowsHide: true, maxBuffer });
    let out = '';
    let err = '';
    child.stdout.setEncoding('utf8');
    child.stderr.setEncoding('utf8');
    child.stdout.on('data', (chunk) => { out += chunk; });
    child.stderr.on('data', (chunk) => { err += chunk; });
    child.on('error', (error) => fail(new PluginError('graph.gitUnavailable', `Cannot start git: ${error.message}`)));
    child.on('close', (code) => {
      if (code !== 0) fail(new PluginError('graph.gitFailed', err.trim() || `git exited with ${code}`));
      else done(out);
    });
  });
}

function isDir(path) {
  try { return statSync(path).isDirectory(); } catch { return false; }
}

function hasGit(path) {
  return existsSync(join(path, '.git'));
}

async function discoverRepos() {
  const { contexts } = await backend.host.workspace.listContexts();
  const roots = [];
  for (const context of contexts) {
    if (context.authority?.kind !== 'local') continue;
    for (const folder of context.folders || []) {
      if (folder.path) roots.push(folder.path);
    }
  }
  const found = new Map();
  for (const root of roots) {
    const candidates = [root];
    // One level below the opened folder covers the common multi-project layout.
    try {
      for (const name of readdirSync(root)) {
        const child = join(root, name);
        if (isDir(child) && name !== '.git') candidates.push(child);
      }
    } catch { /* unreadable root: the root itself may still be a repo */ }
    for (const path of candidates) {
      if (!hasGit(path) || found.has(resolve(path))) continue;
      found.set(resolve(path), {
        id: resolve(path),
        name: path === root ? (root.split(/[\\/]/).pop() || root) : path.slice(root.length + 1),
        path: resolve(path),
        root,
      });
    }
  }
  return [...found.values()].sort((a, b) => a.path.localeCompare(b.path));
}

function parseRefs(decoration) {
  return decoration.split(',').map((part) => part.trim()).filter(Boolean).map((part) => {
    let kind = 'ref';
    let name = part;
    if (part.startsWith('HEAD -> ')) { kind = 'head'; name = part.slice(8); }
    else if (part.startsWith('tag: ')) { kind = 'tag'; name = part.slice(5); }
    else if (part.includes('/')) kind = 'remote';
    return { kind, name };
  });
}

async function readLog(repo, limit) {
  const count = Math.min(Math.max(Number(limit) || 200, 1), 2000);
  const format = ['%H', '%P', '%an', '%aI', '%s', '%D'].join(FIELD) + RECORD;
  const text = await runGit(repo, [
    'log', `--max-count=${count}`, `--pretty=format:${format}`, '--no-color',
  ]);
  const commits = [];
  for (const chunk of text.split(RECORD)) {
    const fields = chunk.trim().split(FIELD);
    if (fields.length < 6) continue;
    const [hash, parents, author, date, subject, decoration] = fields;
    commits.push({
      hash,
      parents: parents ? parents.split(' ').filter(Boolean) : [],
      refs: parseRefs(decoration),
      author,
      date,
      subject,
    });
  }
  return commits;
}

async function readCommit(repo, hash) {
  if (!/^[0-9a-f]{7,40}$/i.test(hash)) throw new PluginError('graph.badHash', 'Invalid commit id');
  const meta = await runGit(repo, ['show', '-s', `--format=%H${FIELD}%an${FIELD}%aI${FIELD}%B`, '--no-color', hash]);
  const [full, author, date, ...message] = meta.trimEnd().split(FIELD);
  const changed = await runGit(repo, ['show', '--format=', '--name-status', '--no-renames', '--no-color', hash]);
  const files = [];
  for (const line of changed.split('\n')) {
    const parts = line.split('\t');
    if (parts.length >= 2 && /^[AMDRT]$/.test(parts[0])) files.push({ status: parts[0], path: parts.slice(1).join('\t') });
  }
  return { hash: full || hash, author: author || '', date: date || '', message: message.join(FIELD).trim(), files };
}

const backend = createBackend({ name: 'git-graph-lite', version: '0.1.0' });

backend
  .function('graph.repos', async () => ({ repos: await discoverRepos() }))
  .function('graph.log', async (params) => {
    const repo = params?.repo;
    if (typeof repo !== 'string' || !isDir(repo)) throw new PluginError('graph.badRepo', 'Unknown repository');
    return { commits: await readLog(repo, params?.limit) };
  })
  .function('graph.commit', async (params) => {
    const repo = params?.repo;
    if (typeof repo !== 'string' || !isDir(repo)) throw new PluginError('graph.badRepo', 'Unknown repository');
    return readCommit(repo, String(params?.hash || ''));
  });

backend.listen();
