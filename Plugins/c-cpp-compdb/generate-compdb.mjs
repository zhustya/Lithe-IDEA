// Generates compile_commands.json for C/C++ workspaces that lack one.
// Supports CMake (CMAKE_EXPORT_COMPILE_COMMANDS) and Eclipse/MRS makefiles
// (obj/**/subdir.mk + .cproject). No dependencies outside Node's standard library.

import { execFileSync } from 'node:child_process';
import { existsSync, mkdirSync, readdirSync, readFileSync, statSync, writeFileSync } from 'node:fs';
import { join, resolve, sep } from 'node:path';

const MAX_WALK_DEPTH = 4;
const EXCLUDED_DIRS = new Set([
  '.git', '.worktree', '.worktrees', 'node_modules', 'build', 'dist',
  '.artifacts', 'target', '.vscode', '.idea', '.opencode', '.agents', 'backups', 'worktrees',
]);

function normalize(p) {
  return String(p).replace(/\\/g, '/');
}

function isDir(p) {
  try { return statSync(p).isDirectory(); } catch { return false; }
}

function readText(p) {
  try { return readFileSync(p, 'utf8'); } catch { return ''; }
}

/** Walks a bounded tree and returns directories where predicate(dir) is true. */
function findDirs(root, predicate, depth = 0, out = []) {
  if (depth > MAX_WALK_DEPTH || !isDir(root)) return out;
  if (predicate(root)) out.push(root);
  let names;
  try { names = readdirSync(root); } catch { return out; }
  for (const name of names) {
    if (EXCLUDED_DIRS.has(name) || name.startsWith('.')) continue;
    const child = join(root, name);
    if (isDir(child)) findDirs(child, predicate, depth + 1, out);
  }
  return out;
}

/** Collects all existing relative directory paths below a project root. */
function relativeDirs(projectRoot, depth = 0, out = new Set()) {
  if (depth > 5) return out;
  let names;
  try { names = readdirSync(projectRoot); } catch { return out; }
  for (const name of names) {
    if (name.startsWith('.') || EXCLUDED_DIRS.has(name)) continue;
    const child = join(projectRoot, name);
    if (!isDir(child)) continue;
    out.add(normalize(name));
    for (const sub of relativeDirs(child, depth + 1, new Set())) out.add(normalize(join(name, sub)));
  }
  return out;
}

/**
 * A stale absolute -I path (e.g. a deleted Temp copy of the project) is repaired by
 * matching its longest tail against an existing directory inside the project.
 */
function repairInclude(raw, projectRoot, knownDirs) {
  const value = normalize(raw).replace(/^"(.*)"$/, '$1');
  if (!value) return null;
  if (value.includes(':')) {
    if (existsSync(value)) return value;
    let best = null;
    for (const dir of knownDirs) {
      if (value.endsWith('/' + dir) && (!best || dir.length > best.length)) best = dir;
    }
    if (best) return normalize(join(projectRoot, best));
    return null;
  }
  const candidate = normalize(resolve(projectRoot, value));
  return existsSync(candidate) ? candidate : null;
}

/** Extracts unique compiler flags from one Eclipse/MRS compile rule line. */
function flagsFromRule(line) {
  const tokens = line.trim().replace(/^@/, '').split(/\s+/).filter(Boolean);
  const flags = [];
  for (let i = 1; i < tokens.length; i++) {
    const t = tokens[i];
    // Makefile automatic variables may be quoted ("$@" / "$<") on Windows.
    if (/^"?\$[@<]"?$/.test(t)) continue;
    // Codegen/target-only flags are invalid for the host clangd driver and do not
    // affect the AST; includes, defines and -std are what clangd needs.
    if (/^-m/.test(t) || t.startsWith('--param=')) continue;
    if (t.startsWith('-M')) continue;
    if (t.startsWith('-MF') || t.startsWith('-MT') || t.startsWith('-o') || t === '-c') continue;
    if (/^[/-]?[@$]/.test(t) && !t.startsWith('-')) continue;
    if (t === '-I' || t === '-D' || t === '-include' || t === '-isystem') {
      if (i + 1 < tokens.length) flags.push(t, tokens[++i]);
      continue;
    }
    flags.push(t);
  }
  return flags;
}

/** Parses all obj/**\/subdir.mk files of one Eclipse/MRS project. */
function parseEclipseProject(projectRoot) {
  const mkRoot = join(projectRoot, 'obj');
  if (!isDir(mkRoot) || !existsSync(join(projectRoot, '.cproject'))) return null;
  const knownDirs = relativeDirs(projectRoot);
  // subdir.mk files live directly under obj/<group>/subdir.mk (bounded depth 3).
  const mks = [];
  const walkMk = (dir, depth) => {
    if (depth > 3 || !isDir(dir)) return;
    for (const name of readdirSync(dir)) {
      if (name.startsWith('.') || EXCLUDED_DIRS.has(name)) continue;
      const p = join(dir, name);
      if (isDir(p)) walkMk(p, depth + 1);
      else if (name === 'subdir.mk') mks.push(p);
    }
  };
  walkMk(mkRoot, 0);
  if (!mks.length) return null;

  const entries = [];
  const seen = new Set();
  for (const mk of mks) {
    const text = readText(mk);
    const rule = text.split(/\r?\n/).find((l) => /\b(gcc|clang|cc|c\+\+)\b/.test(l) && l.includes('-c '));
    if (!rule) continue;
    const rawFlags = flagsFromRule(rule);
    const flags = [];
    for (let i = 0; i < rawFlags.length; i++) {
      const f = rawFlags[i];
      if (f === '-I' || f === '-isystem') {
        const fixed = repairInclude(rawFlags[++i], projectRoot, knownDirs);
        if (fixed) flags.push(f === '-I' ? '-I' : '-isystem', fixed);
        continue;
      }
      if (f.startsWith('-I')) {
        const fixed = repairInclude(f.slice(2), projectRoot, knownDirs);
        if (fixed) flags.push('-I' + fixed);
        continue;
      }
      flags.push(f);
    }
    const sources = [];
    for (const line of text.split(/\r?\n/)) {
      if (!/^\s*C_SRCS\s*\+=/i.test(line)) continue;
      const block = [line.replace(/^[^=]*=\s*/, '')];
      // Eclipse wraps with trailing backslashes; collect following lines until blank/non-continuation.
      const lines = text.split(/\r?\n/);
      const idx = lines.indexOf(line);
      for (let j = idx + 1; j < lines.length; j++) {
        const next = lines[j].trim();
        if (!next) break;
        block.push(next.replace(/\\$/, ''));
        if (!lines[j].trimEnd().endsWith('\\')) break;
      }
      for (const token of block.join(' ').split(/\s+/)) {
        if (/\.c$/i.test(token)) sources.push(token.replace(/\\/g, '/'));
      }
    }
    const mkDir = normalize(mk).replace(/\/[^\/]*$/, '');
    const relGroup = mkDir.replace(normalize(projectRoot) + '/', '').replace(/^obj\//, '');
    for (const src of new Set(sources)) {
      const absolute = normalize(resolve(projectRoot, relGroup, src));
      if (seen.has(absolute) || !existsSync(absolute)) continue;
      seen.add(absolute);
      entries.push({
        directory: normalize(projectRoot),
        command: ['clang', ...flags, '-c', absolute].join(' '),
        file: absolute,
      });
    }
  }
  return entries.length ? entries : null;
}

/** Configures CMake once and copies its database to the project root. */
function generateForCMake(projectRoot) {
  const outDir = join(projectRoot, '.lithe-compdb-cmake');
  try {
    mkdirSync(outDir, { recursive: true });
    execFileSync('cmake', [
      '-S', projectRoot, '-B', outDir,
      '-DCMAKE_EXPORT_COMPILE_COMMANDS=ON',
    ], { stdio: 'ignore', timeout: 120_000, windowsHide: true });
    const generated = join(outDir, 'compile_commands.json');
    if (!existsSync(generated)) return false;
    writeFileSync(join(projectRoot, 'compile_commands.json'), readFileSync(generated));
    return true;
  } catch {
    return false;
  }
}

function writeDatabase(projectRoot, entries) {
  const target = join(projectRoot, 'compile_commands.json');
  writeFileSync(target, JSON.stringify(entries, null, 2) + '\n', 'utf8');
  return target;
}

/**
 * Ensures every C/C++ subproject under `root` has a compilation database.
 * Returns a short report array for logging.
 */
export function ensureDatabases(root) {
  const report = [];
  if (!root || !isDir(root)) return report;

  // 1. CMake projects.
  for (const dir of findDirs(root, (d) => existsSync(join(d, 'CMakeLists.txt')))) {
    if (existsSync(join(dir, 'compile_commands.json'))) continue;
    report.push(generateForCMake(dir)
      ? `cmake: ${normalize(dir)}`
      : `cmake-failed: ${normalize(dir)}`);
  }

  // 2. Eclipse / MRS makefile projects.
  for (const dir of findDirs(root, (d) =>
    existsSync(join(d, '.cproject')) && isDir(join(d, 'obj')))) {
    if (existsSync(join(dir, 'compile_commands.json'))) continue;
    const entries = parseEclipseProject(dir);
    if (entries) {
      writeDatabase(dir, entries);
      report.push(`eclipse-mrs: ${normalize(dir)} (${entries.length} files)`);
    } else {
      report.push(`eclipse-mrs-failed: ${normalize(dir)}`);
    }
  }
  return report;
}

export { ensureDatabases as default };
