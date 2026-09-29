const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawnSync } = require('node:child_process');
const asar = require('@electron/asar');

test('ASAR syntax gate checks new helper modules and hides invalid private source', async () => {
  const temporary = fs.mkdtempSync(path.join(os.tmpdir(), 'csr-asar-added-'));
  try {
    const original = path.join(temporary, 'original');
    const candidate = path.join(temporary, 'candidate');
    fs.mkdirSync(original); fs.mkdirSync(candidate);
    for (const directory of [original, candidate]) fs.writeFileSync(path.join(directory, 'main.js'), 'const original = true;');
    const helper = path.join(candidate, 'shared.cjs');
    fs.writeFileSync(helper, 'module.exports = { shared: true };');
    const sourceAsar = path.join(temporary, 'original.asar');
    const goodAsar = path.join(temporary, 'good.asar');
    const badAsar = path.join(temporary, 'bad.asar');
    await asar.createPackage(original, sourceAsar);
    await asar.createPackage(candidate, goodAsar);
    const script = path.join(__dirname, '..', 'scripts', 'check_patched_asar.cjs');
    const good = spawnSync(process.execPath, [script, sourceAsar, goodAsar], { encoding: 'utf8', windowsHide: true });
    assert.equal(good.status, 0, good.stderr);
    assert.match(good.stdout, /1 modified JavaScript/);
    fs.writeFileSync(helper, 'privateMarker_DO_NOT_ECHO function invalid {');
    await asar.createPackage(candidate, badAsar);
    const bad = spawnSync(process.execPath, [script, sourceAsar, badAsar], { encoding: 'utf8', windowsHide: true });
    assert.notEqual(bad.status, 0);
    assert.match(bad.stderr, /failed syntax validation/);
    assert.doesNotMatch(bad.stderr + bad.stdout, /privateMarker_DO_NOT_ECHO/);
  } finally { fs.rmSync(temporary, { recursive: true, force: true }); }
});
