import assert from 'node:assert/strict';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { ensureDatabases } from '../generate-compdb.mjs';

function fixture() {
  const root = mkdtempSync(join(tmpdir(), 'lithe-compdb-'));
  mkdirSync(join(root, 'HAL/include'), { recursive: true });
  mkdirSync(join(root, 'src/App'), { recursive: true });
  mkdirSync(join(root, 'obj/HAL'), { recursive: true });
  writeFileSync(join(root, '.cproject'), '<cproject/>');
  writeFileSync(join(root, 'HAL/include/hal.h'), '#define HAL 1\n');
  writeFileSync(join(root, 'src/App/main.c'), '#include "hal.h"\nint main(void){return 0;}\n');
  // Stale absolute include path + automatic variables, as MRS writes them.
  writeFileSync(join(root, 'obj/HAL/subdir.mk'), [
    'C_SRCS += \\',
    ' ../src/App/main.c',
    '',
    'HAL/%.o: ../src/App/%.c',
    '\t@\triscv-wch-elf-gcc -march=rv32imc_zba -Os -DDEBUG=1 -I"C:/Temp/deleted_copy/HAL/include" -std=gnu99 -MMD -MP -MF"$(@:%.o=%.d)" -c -o "$@" "$<"',
    '',
  ].join('\n'));
  return root;
}

test('generates a database from Eclipse/MRS makefiles', () => {
  const root = fixture();
  try {
    const report = ensureDatabases(root);
    assert.ok(report.some((line) => line.startsWith('eclipse-mrs: ')), report.join('; '));
    const db = JSON.parse(readFileSync(join(root, 'compile_commands.json'), 'utf8'));
    assert.equal(db.length, 1);
    assert.match(db[0].file, /src[/\\]App[/\\]main\.c$/);
    assert.match(db[0].command, /-I[^\s]*HAL[/\\]include/);
    assert.doesNotMatch(db[0].command, /rv32/);
    assert.doesNotMatch(db[0].command, /\$[@<]/);
    assert.doesNotMatch(db[0].command, /-MMD/);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('does not overwrite an existing database', () => {
  const root = fixture();
  try {
    const target = join(root, 'compile_commands.json');
    writeFileSync(target, '[]');
    ensureDatabases(root);
    assert.equal(readFileSync(target, 'utf8'), '[]');
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
