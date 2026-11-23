// Git Graph (Native View): read-only git history drawn with Lithe plugin views.
// No web view, no WebView lifecycle issues; host renders the canvas natively.
import { spawn } from 'node:child_process';
import { existsSync, readdirSync, statSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { createBackend, PluginError } from '../vendor/lithe-plugin-sdk/src/index.mjs';

const FIELD = '\x1f';
const RECORD = '\x1e';
const ROW = 26;
const LANE = 14;
const COLORS = ['#e06060', '#60a0e0', '#60c090', '#c090e0', '#e0b040', '#40b0c0', '#d070a0', '#90a0b0'];

function runGit(repo, args) {
  return new Promise((done, fail) => {
    const child = spawn('git', args, { cwd: repo, windowsHide: true, maxBuffer: 16 * 1024 * 1024 });
    let out = '', err = '';
    child.stdout.setEncoding('utf8');
    child.stderr.setEncoding('utf8');
    child.stdout.on('data', (c) => { out += c; });
    child.stderr.on('data', (c) => { err += c; });
    child.on('error', (e) => fail(new PluginError('graph.gitUnavailable', `Cannot start git: ${e.message}`)));
    child.on('close', (code) => code === 0 ? done(out) : fail(new PluginError('graph.gitFailed', err.trim() || `git exited ${code}`)));
  });
}

const isDir = (p) => { try { return statSync(p).isDirectory(); } catch { return false; } };
const hasGit = (p) => existsSync(join(p, '.git'));

async function discoverRepos() {
  const { contexts } = await backend.host.workspace.listContexts();
  const roots = [];
  for (const context of contexts) {
    if (context.authority?.kind !== 'local') continue;
    for (const folder of context.folders || []) if (folder.path) roots.push(folder.path);
  }
  const found = new Map();
  for (const root of roots) {
    const candidates = [root];
    try {
      for (const name of readdirSync(root)) {
        const child = join(root, name);
        if (isDir(child) && name !== '.git') candidates.push(child);
      }
    } catch { /* root itself may still be a repo */ }
    for (const path of candidates) {
      if (!hasGit(path) || found.has(resolve(path))) continue;
      found.set(resolve(path), {
        id: resolve(path),
        name: path === root ? (root.split(/[\\/]/).pop() || root) : path.slice(root.length + 1),
        path: resolve(path),
      });
    }
  }
  return [...found.values()].sort((a, b) => a.path.localeCompare(b.path));
}

function parseRefs(decoration) {
  return decoration.split(',').map((s) => s.trim()).filter(Boolean).map((part) => {
    if (part.startsWith('HEAD -> ')) return { kind: 'head', name: part.slice(8) };
    if (part.startsWith('tag: ')) return { kind: 'tag', name: part.slice(5) };
    if (part.includes('/')) return { kind: 'remote', name: part };
    return { kind: 'branch', name: part };
  });
}

async function readLog(repo, limit) {
  const count = Math.min(Math.max(Number(limit) || 200, 1), 1000);
  const format = ['%H', '%P', '%an', '%aI', '%s', '%D'].join(FIELD) + RECORD;
  // --all includes commits from every branch/tag, not just HEAD's ancestry;
  // --topo-order keeps children next to their parents for clean lanes.
  const text = await runGit(repo, ['log', '--all', '--topo-order', `--max-count=${count}`, `--pretty=format:${format}`, '--no-color']);
  const commits = [];
  for (const chunk of text.split(RECORD)) {
    const f = chunk.trim().split(FIELD);
    if (f.length < 6) continue;
    commits.push({ hash: f[0], parents: f[1] ? f[1].split(' ').filter(Boolean) : [], refs: parseRefs(f[5]), author: f[2], date: f[3], subject: f[4] });
  }
  return commits;
}

async function readCommit(repo, hash) {
  if (!/^[0-9a-f]{7,40}$/i.test(hash)) return null;
  try {
    const meta = await runGit(repo, ['show', '-s', `--format=%H${FIELD}%an${FIELD}%aI${FIELD}%B`, '--no-color', hash]);
    const [full, author, date, ...message] = meta.trimEnd().split(FIELD);
    const changed = await runGit(repo, ['show', '--format=', '--name-status', '--no-renames', '--no-color', hash]);
    const files = [];
    for (const line of changed.split('\n')) {
      const parts = line.split('\t');
      if (parts.length >= 2 && /^[AMDRT]$/.test(parts[0])) files.push({ status: parts[0], path: parts.slice(1).join('\t') });
    }
    return { hash: full, author, date, message: message.join(FIELD).trim(), files };
  } catch { return null; }
}

function layout(list) {
  const lanes = [];
  const byHash = new Map(list.map((c) => [c.hash, c]));
  for (const commit of list) {
    let lane = lanes.indexOf(commit.hash);
    if (lane < 0) { lane = lanes.indexOf(null); if (lane < 0) lane = lanes.length; }
    commit.lane = lane;
    lanes[lane] = null;
    // A parent already tracked in another lane must not be registered twice:
    // a duplicate makes the stale lane keep the same hash forever, so later
    // commits are assigned to the wrong lane and their edges/dots look broken.
    const first = commit.parents[0];
    if (first && !lanes.includes(first)) lanes[lane] = first;
    for (const parent of commit.parents.slice(1)) {
      if (lanes.includes(parent)) continue;
      const free = lanes.indexOf(null);
      lanes[free < 0 ? lanes.length : free] = parent;
    }
  }
  return { byHash, laneCount: Math.max(lanes.length, 1) };
}

const states = new Map();
function stateOf(event) {
  if (!states.has(event.instance)) {
    states.set(event.instance, { repo: '', limit: '200', search: '', selected: '', repos: [], commits: [], detail: null });
  }
  return states.get(event.instance);
}

const CHUNK = 125;
function canvasesFor(s, width, query) {
  const list = query
    ? s.commits.filter((c) => c.subject.toLowerCase().includes(query) || c.author.toLowerCase().includes(query) || c.hash.includes(query))
    : s.commits;
  const { laneCount } = layout(list);
  const w = Math.max(Number(width) || 900, 640);
  const refsStart = laneCount * LANE + 12;
  const scenes = [];

  for (let start = 0; start < list.length; start += CHUNK) {
    const chunk = list.slice(start, start + CHUNK);
    const local = new Map(chunk.map((c, i) => [c.hash, i]));
    const contentHeight = Math.max(chunk.length * ROW + 8, 40);
    const nodes = [];

    for (let i = 0; i < chunk.length; i++) {
      const commit = chunk[i];
      const x = commit.lane * LANE + LANE / 2 + 8;
      const y = i * ROW + ROW / 2;
      for (const parentHash of commit.parents) {
        const pi = local.get(parentHash);
        if (pi === undefined) continue;
        const px = chunk[pi].lane * LANE + LANE / 2 + 8;
        const py = pi * ROW + ROW / 2;
        // Git Graph style: jump to the parent's lane right after the child
        // (the merge point), then run straight down that lane. The parent's
        // color keeps a branch line continuous through merges.
        let d;
        if (px === x) {
          d = `M ${x} ${y} L ${x} ${py}`;
        } else {
          const dir = px > x ? 1 : -1;
          const bend = Math.min(6, Math.abs(px - x));
          d = `M ${x} ${y} L ${x} ${y + 5} Q ${x} ${y + 11} ${x + dir * bend} ${y + 11} L ${px} ${y + 11} L ${px} ${py}`;
        }
        nodes.push({
          type: 'path', d: d,
          stroke: COLORS[chunk[pi].lane % COLORS.length], width: 2,
        });
      }
    }

    for (let i = 0; i < chunk.length; i++) {
      const commit = chunk[i];
      const x = commit.lane * LANE + LANE / 2 + 8;
      const children = [];
      const selected = commit.hash === s.selected;
      if (selected) {
        children.push({ type: 'rect', x: 0, y: 0, w: w, h: ROW, fill: '$hover', opacity: 0.45 });
      }
      if (selected) {
        // Git Graph marks the selected commit with a hollow square.
        children.push({ type: 'rect', x: x - 5, y: ROW / 2 - 5, w: 10, h: 10, radius: 1,
          fill: '$background', stroke: '$accent', width: 2 });
      } else {
        children.push({ type: 'rect', x: x - 5, y: ROW / 2 - 5, w: 10, h: 10, radius: 5,
          fill: COLORS[commit.lane % COLORS.length], stroke: commit.refs.some((r) => r.kind === 'head') ? '$error' : undefined, width: 2 });
      }

      let refX = refsStart;
      for (const ref of commit.refs) {
        const label = ref.name.length > 22 ? ref.name.slice(0, 21) + '…' : ref.name;
        const badgeW = label.length * 6.5 + 12;
        const fill = ref.kind === 'head' ? '$error' : ref.kind === 'tag' ? '$warning' : ref.kind === 'remote' ? '#6b46c1' : '$accent';
        children.push({ type: 'rect', x: refX, y: 6, w: badgeW, h: 14, radius: 7, fill: fill });
        children.push({ type: 'text', x: refX + 6, y: 17, style: 'caption', text: label, color: '#ffffff' });
        refX += badgeW + 6;
      }

      children.push({ type: 'text', x: Math.max(refX, refsStart) + 8, y: 17, style: 'body', text: commit.subject, maxWidth: Math.max(w - refsStart - 240, 120) });
      children.push({ type: 'text', x: w - 100, y: 17, style: 'caption', text: commit.author.slice(0, 14), color: '$textSecondary', align: 'end' });
      children.push({ type: 'text', x: w - 8, y: 17, style: 'caption', text: commit.date.slice(0, 10), color: '$textMuted', align: 'end' });

      nodes.push({
        type: 'group', id: 'commit-' + commit.hash.slice(0, 12), x: 0, y: i * ROW, w: w, h: ROW,
        label: commit.subject, on: ['click'], children: children,
      });
    }

    scenes.push({
      type: 'canvas', id: 'graph-' + start,
      label: `Commits ${start + 1}–${start + chunk.length}`,
      // Core caps a canvas widget at 2000 points; taller scenes pan inside it.
      height: Math.min(contentHeight, 2000),
      viewBox: [0, 0, w, contentHeight],
      ...(contentHeight > 2000 ? { pan: true } : {}),
      on: ['resize'], nodes: nodes,
    });
  }
  return scenes;
}

function viewFor(s, event) {
  const width = typeof event.width === 'number' ? event.width : undefined;
  const controls = [
    {
      type: 'select', id: 'repo', label: 'Repository', value: s.repo,
      options: s.repos.map((r) => ({ value: r.id, label: r.name })),
    },
    { type: 'button', id: 'refresh', text: 'Refresh' },
    { type: 'select', id: 'limit', label: 'Count', value: s.limit, options: ['100', '200', '500', '1000'].map((v) => ({ value: v, label: v })) },
    { type: 'textField', id: 'search', label: 'Search', value: s.search, placeholder: 'subject / author / hash', width: 'fill' },
  ];
  const nodes = [{ type: 'row', children: controls }];

  if (!s.repos.length) {
    nodes.push({ type: 'banner', text: 'No Git repositories found in the open workspace.', tone: 'warning' });
    return { view: nodes };
  }
  nodes.push({ type: 'label', text: `${s.commits.length} commits · ${s.repos.find((r) => r.id === s.repo)?.name || ''}`, tone: 'muted' });
  nodes.push(...canvasesFor(s, width, s.search.trim().toLowerCase()));

  if (s.detail) {
    const d = s.detail;
    nodes.push({ type: 'section', title: 'Commit', children: [
      { type: 'keyValue', items: [
        { label: 'Hash', value: d.hash.slice(0, 12) },
        { label: 'Author', value: d.author },
        { label: 'Date', value: d.date },
      ] },
      { type: 'label', text: d.message, style: 'body' },
      { type: 'label', text: d.files.length ? 'Changed files' : 'No changed files', style: 'strong' },
      ...d.files.slice(0, 80).map((f) => ({ type: 'label', text: `${f.status}  ${f.path}`, style: 'mono', info: f.path })),
    ] });
  }
  return { view: nodes };
}

async function refresh(s, event) {
  s.repos = await discoverRepos();
  if (!s.repos.some((r) => r.id === s.repo)) s.repo = s.repos[0]?.id || '';
  const repo = s.repos.find((r) => r.id === s.repo);
  s.commits = repo ? await readLog(repo.path, s.limit) : [];
  s.detail = null;
  return viewFor(s, event);
}

const backend = createBackend({ name: 'git-graph-native', version: '0.1.0' });

backend.view('graph.draw', async (event) => {
  const s = stateOf(event);
  if (event.event === 'close') { states.delete(event.instance); return {}; }
  if (event.event === 'open') return refresh(s, event);
  if (event.event === 'change') {
    if (event.node === 'repo') s.repo = String(event.value || '');
    else if (event.node === 'limit') s.limit = String(event.value || '200');
    else if (event.node === 'search') s.search = String(event.value || '');
    else return {};
    if (event.node !== 'search') return refresh(s, event);
    return viewFor(s, event);
  }
  if (event.event === 'click') {
    if (event.node === 'refresh') return refresh(s, event);
    if (String(event.canvas || '').startsWith('graph-') && String(event.node || '').startsWith('commit-')) {
      const prefix = event.node.slice(7);
      const commit = s.commits.find((c) => c.hash.startsWith(prefix));
      if (!commit) return {};
      s.selected = commit.hash;
      const repo = s.repos.find((r) => r.id === s.repo);
      s.detail = repo ? await readCommit(repo.path, commit.hash) : null;
      return viewFor(s, event);
    }
  }
  if (event.event === 'resize' || event.event === 'viewport') return viewFor(s, event);
  return {};
});

backend.listen();
