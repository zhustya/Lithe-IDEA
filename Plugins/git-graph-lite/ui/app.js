// Git Graph Lite web panel. All data comes from the plugin backend through
// window.lithe's whitelisted bridge; no network or inline scripts are used.
(function () {
  'use strict';

  const SVG = 'http://www.w3.org/2000/svg';
  const ROW = 26;
  const LANE = 18;
  const COLORS = ['#e06060', '#60a0e0', '#60c090', '#c090e0', '#e0b040', '#40b0c0', '#d070a0', '#90a0b0'];

  const el = {
    repo: document.getElementById('repo'),
    refresh: document.getElementById('refresh'),
    limit: document.getElementById('limit'),
    search: document.getElementById('search'),
    status: document.getElementById('status'),
    graph: document.getElementById('graph'),
    detail: document.getElementById('detail'),
    detailHead: document.getElementById('detail-head'),
    detailMessage: document.getElementById('detail-message'),
    detailFiles: document.getElementById('detail-files'),
  };

  let repos = [];
  let commits = [];
  let selected = null;
  let state = { repo: '' };

  function status(text) { el.status.textContent = text || ''; }

  // The host injects its bridge script before page scripts, but the underlying
  // channel can become available slightly later in some web views. Wait briefly
  // instead of failing with "Cannot read properties of undefined".
  let bridgeReloaded = false;
  function waitForBridge(timeout) {
    const limit = timeout || 8000;
    return new Promise((resolve, reject) => {
      const started = Date.now();
      const check = () => {
        if (window.lithe && typeof window.lithe.request === 'function') return resolve();
        // The injected bootstrap returns early when window.viva is not ready yet.
        // Retry it once from the host-served path after the channel has settled.
        if (!bridgeReloaded && Date.now() - started > 300) {
          bridgeReloaded = true;
          const script = document.createElement('script');
          script.src = '/.lithe-host/bridge.js';
          document.head.appendChild(script);
        }
        if (Date.now() - started >= limit) return reject(new Error('Lithe bridge is unavailable in this page.'));
        setTimeout(check, 50);
      };
      check();
    });
  }

  async function call(fn, params) {
    await waitForBridge();
    const answer = await window.lithe.request('functions.invoke', { function: fn, params: params || {} });
    return answer ? answer.value : undefined;
  }

  function currentRepo() {
    return repos.find((r) => r.id === el.repo.value) || null;
  }

  function layout(list) {
    const lanes = [];
    const byHash = new Map(list.map((c) => [c.hash, c]));
    for (const commit of list) {
      let lane = lanes.indexOf(commit.hash);
      if (lane < 0) {
        lane = lanes.indexOf(null);
        if (lane < 0) lane = lanes.length;
      }
      commit.lane = lane;
      lanes[lane] = null;
      commit.parents.forEach((parent, index) => {
        if (lanes.includes(parent)) return;
        const free = lanes.indexOf(null);
        const slot = index === 0 ? lane : (free < 0 ? lanes.length : free);
        lanes[slot] = parent;
      });
    }
    return { byHash, laneCount: Math.max(lanes.length, 1) };
  }

  function svgEl(name, attrs) {
    const node = document.createElementNS(SVG, name);
    for (const key of Object.keys(attrs || {})) node.setAttribute(key, String(attrs[key]));
    return node;
  }

  function render(list) {
    el.graph.replaceChildren();
    if (!list.length) {
      const hint = document.createElement('div');
      hint.className = 'hint';
      hint.textContent = 'No commits in this repository.';
      el.graph.appendChild(hint);
      return;
    }
    const { byHash, laneCount } = layout(list);
    const width = laneCount * LANE + 2;
    const height = list.length * ROW;

    const svg = svgEl('svg', { width, height, class: 'lanes', 'aria-hidden': 'true' });
    list.forEach((commit, index) => {
      const x = commit.lane * LANE + LANE / 2;
      const y = index * ROW + ROW / 2;
      for (const parentHash of commit.parents) {
        const parent = byHash.get(parentHash);
        if (!parent || parent.lane === undefined) continue;
        const px = parent.lane * LANE + LANE / 2;
        const py = list.indexOf(parent) * ROW + ROW / 2;
        const path = svgEl('path', {
          d: `M ${x} ${y} C ${x} ${(y + py) / 2}, ${px} ${(y + py) / 2}, ${px} ${py}`,
          stroke: COLORS[commit.lane % COLORS.length],
          'stroke-width': 2,
          fill: 'none',
        });
        svg.appendChild(path);
      }
      svg.appendChild(svgEl('circle', {
        cx: x, cy: y, r: 4,
        fill: COLORS[commit.lane % COLORS.length],
        stroke: commit.refs.some((r) => r.kind === 'head') ? '#fff' : 'none',
        'stroke-width': 1.5,
      }));
    });

    const listBox = document.createElement('div');
    listBox.style.position = 'absolute';
    listBox.style.left = width + 'px';
    listBox.style.top = '0';
    listBox.style.right = '0';
    list.forEach((commit) => {
      const row = document.createElement('div');
      row.className = 'row' + (selected === commit.hash ? ' selected' : '');
      row.title = commit.hash + '\n' + commit.author + '\n' + commit.date;

      const refs = document.createElement('div');
      refs.className = 'refs';
      for (const ref of commit.refs) {
        const badge = document.createElement('span');
        badge.className = 'ref ' + (['head', 'branch', 'remote', 'tag'].includes(ref.kind) ? ref.kind : 'branch');
        badge.textContent = ref.name;
        refs.appendChild(badge);
      }

      const subject = document.createElement('span');
      subject.className = 'subject';
      subject.textContent = commit.subject;
      const author = document.createElement('span');
      author.className = 'author';
      author.textContent = commit.author;
      const date = document.createElement('span');
      date.className = 'date';
      date.textContent = commit.date.slice(0, 10);

      row.append(refs, subject, author, date);
      row.addEventListener('click', () => showCommit(commit.hash));
      listBox.appendChild(row);
    });

    el.graph.style.height = height + 'px';
    el.graph.appendChild(svg);
    el.graph.appendChild(listBox);
  }

  function filterAndRender() {
    const query = el.search.value.trim().toLowerCase();
    const shown = query
      ? commits.filter((c) => c.subject.toLowerCase().includes(query) || c.author.toLowerCase().includes(query) || c.hash.includes(query))
      : commits;
    render(shown);
  }

  async function showCommit(hash) {
    selected = hash;
    const repo = currentRepo();
    if (!repo) return;
    try {
      const detail = await call('graph.commit', { repo: repo.path, hash });
      el.detail.hidden = false;
      el.detailHead.textContent = `${detail.hash.slice(0, 10)}  ${detail.author}  ${detail.date}`;
      el.detailMessage.textContent = detail.message;
      el.detailFiles.replaceChildren();
      if (!detail.files.length) {
        const none = document.createElement('div');
        none.className = 'hint';
        none.textContent = 'No changed files.';
        el.detailFiles.appendChild(none);
      }
      for (const file of detail.files) {
        const line = document.createElement('div');
        line.className = 'file';
        const st = document.createElement('span');
        st.className = 'st';
        st.textContent = file.status;
        const path = document.createElement('span');
        path.textContent = file.path;
        line.append(st, path);
        line.addEventListener('click', async () => {
          const full = repo.path.replace(/\\/g, '/') + '/' + file.path;
          try {
            await waitForBridge();
            await window.lithe.request('workspace.openFile', { path: full });
          } catch (err) {
            status(err.message || String(err));
          }
        });
        el.detailFiles.appendChild(line);
      }
      filterAndRender();
    } catch (err) {
      status(err.message || String(err));
    }
  }

  async function loadGraph() {
    const repo = currentRepo();
    if (!repo) return;
    status('Loading commits…');
    try {
      const answer = await call('graph.log', { repo: repo.path, limit: Number(el.limit.value) });
      commits = answer.commits || [];
      filterAndRender();
      status(`${commits.length} commits · ${repo.name}`);
    } catch (err) {
      status(err.message || String(err));
    }
  }

  async function loadRepos() {
    status('Finding repositories…');
    try {
      const answer = await call('graph.repos', {});
      repos = answer.repos || [];
      el.repo.replaceChildren();
      for (const repo of repos) {
        const option = document.createElement('option');
        option.value = repo.id;
        option.textContent = repo.name;
        el.repo.appendChild(option);
      }
      if (repos.length) {
        const previous = repos.find((r) => r.id === state.repo);
        el.repo.value = previous ? previous.id : repos[0].id;
      }
      if (repos.length) await loadGraph();
      else status('No Git repositories found in the open workspace.');
    } catch (err) {
      status(err.message || String(err));
    }
  }

  el.repo.addEventListener('change', () => {
    state.repo = el.repo.value;
    if (window.lithe && typeof window.lithe.request === 'function') {
      window.lithe.request('ui.setState', { state: JSON.stringify(state) }).catch(() => {});
    }
    el.detail.hidden = true;
    loadGraph();
  });
  el.refresh.addEventListener('click', () => { el.detail.hidden = true; loadGraph(); });
  el.limit.addEventListener('change', loadGraph);
  el.search.addEventListener('input', filterAndRender);

  if (window.lithe && typeof window.lithe.on === 'function') {
    window.lithe.on('ui.themeChanged', () => filterAndRender());
  }

  (async function start() {
    try {
      await waitForBridge();
      const saved = await window.lithe.request('ui.getState', {});
      if (saved && saved.state) state = JSON.parse(saved.state) || state;
    } catch (error) {
      status(error.message || String(error));
    }
    await loadRepos();
  })();
})();
