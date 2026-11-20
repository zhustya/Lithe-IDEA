// Packages the declarative plugin into a zip installable via "Install Plugin from Disk".
// Run: node package.mjs
import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const dir = dirname(fileURLToPath(import.meta.url));
const manifest = JSON.parse(readFileSync(join(dir, 'plugin.json'), 'utf8'));
const out = join(dirname(dir), `c-cpp-compdb-${manifest.version}.zip`);
const files = ['plugin.json', 'clangd-wrapper.mjs', 'generate-compdb.mjs', 'README.md'];

if (process.platform === 'win32') {
  const list = files.map((f) => `'${join(dir, f)}'`).join(',');
  execFileSync('powershell', [
    '-NoProfile', '-Command',
    `Compress-Archive -Path ${list} -DestinationPath '${out}' -Force`
  ], { stdio: 'inherit' });
} else {
  execFileSync('zip', ['-j', out, ...files.map((f) => join(dir, f))], { stdio: 'inherit' });
}
console.log('Wrote', out);
