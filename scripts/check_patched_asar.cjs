// Parse every modified JavaScript bundle before a patched app is published.
const asar = require('@electron/asar');
const {spawnSync} = require('node:child_process');
const path = require('node:path');

const [source, candidate] = process.argv.slice(2);
if (!source || !candidate) throw Error('Provide the official and candidate app.asar paths');
let checked = 0;
const normalizedName = entry => entry.replace(/^[\\/]/, '').split(/[\\/]/).join(path.sep);
const originalEntries = new Set(asar.listPackage(source).map(normalizedName));
for (const entry of asar.listPackage(candidate)) {
  if (!/\.(?:c|m)?js$/.test(entry)) continue;
  const name = normalizedName(entry);
  const content = asar.extractFile(candidate, name);
  if (originalEntries.has(name) && content.equals(asar.extractFile(source, name))) continue;
  const result = spawnSync(process.execPath, ['--input-type=module', '--check'], {
    input: content,
    windowsHide: true,
    timeout: 30000,
    maxBuffer: 1024 * 1024,
  });
  // Do not echo parser stderr: injected bundles contain the private API token.
  if (result.error || result.status !== 0) throw Error(`Patched bundle failed syntax validation: ${name}`);
  checked++;
}
if (checked === 0) throw Error('No modified JavaScript bundles were verified');
console.log(`PASS: ${checked} modified JavaScript bundles parse; private source was not printed.`);
