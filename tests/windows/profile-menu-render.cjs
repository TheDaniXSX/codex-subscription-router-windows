// Offline render contract against a real patched ASAR; no network or saved-state writes.
const assert = require('node:assert/strict');
const vm = require('node:vm');
const path = require('node:path');
async function main() {
  const asar = await import('@electron/asar');
  const archive = process.argv[2];
  assert.ok(archive, 'Usage: node tests/windows/profile-menu-render.cjs <patched app.asar>');
  const entries = asar.listPackage(archive).map(p=>p.replace(/\\/g,'/'));
  const primaryPath = entries.find(p => /\/app-primary-[^/]+\.js$/.test(p));
  const initialPath = entries.find(p => /\/app-initial-[^/]+\.js$/.test(p));
  const primary = asar.extractFile(archive, primaryPath.replace(/^\//, '').split('/').join(path.sep)).toString();
  const initial = asar.extractFile(archive, initialPath.replace(/^\//, '').split('/').join(path.sep)).toString();
  const updated = primary.includes('function Cbn(e){');
  if (updated) {
    assert.match(primary, /YG as Zy[,}]/);
    const exported = initial.match(/([\w$]+) as YG[,}]/);
    assert.ok(exported, 'native menu component export is present');
    assert.ok(initial.includes(`function ${exported[1]}(e)`), 'menu binding resolves to a function');
    assert.match(primary, /xK\.jsx\)\(Zy,\{LeftIcon:oT/);
  } else {
  assert.match(primary, /GM as xl[,}]/);
  assert.match(initial, /xeo as GM[,}]/);
  assert.match(initial, /xeo=\(typeof navigator/); // Keybinding map, not a React component.
  assert.match(primary, /YG as Qy[,}]/);
  assert.match(initial, /jea as YG[,}]/);
  assert.match(initial, /function jea\(e\)/);
  assert.match(primary, /dq\.jsx\)\(Qy,\{LeftIcon:CT/); // Native menu uses Qy.
  }
  const start = primary.indexOf('const CODEX_MUX_API =');
  const end = primary.indexOf(updated ? 'function Cbn(e)' : 'function kyn(e)', start);
  assert.ok(start >= 0 && end > start);
  const injected = primary.slice(start, end);
  function render(code, expanded = false) {
    const jsx = (type, props, key) => {
      if (typeof type !== 'function' && typeof type !== 'string') throw Error('Invalid React element type: object (xl keybinding map)');
      return {type, props, key};
    };
    const values = [[{id:'primary', label:'Primary', enabled:true, connected:true, controller:true}], false, '', '', '', null, false, null, {mode:'account',accountId:'primary'}, {enabled:true,records:[]}, expanded];
    let index=0;
    const globals = {
      AbortController, TextDecoder, TextEncoder, setTimeout, clearTimeout, setInterval, clearInterval,
      document:{querySelector:()=>null}, fetch:()=>{throw Error('Network forbidden in this test');},
      dq:{jsx,jsxs:jsx,Fragment:'fragment'}, Zv:()=>{}, fo:()=>({}), HE:{}, MG:()=>{},
      xl:{'Ctrl-a':()=>{}}, Qy:()=>{}, of:{Separator:()=>{}},
      Pyn:{useState:initial=>{const n=index++;if(!(n in values))values[n]=typeof initial==='function'?initial():initial;return [values[n],()=>{}];},useCallback:f=>f,useEffect:()=>{}}
    };
    if (updated) Object.assign(globals, {xK:globals.dq, Obn:globals.Pyn, Hv:globals.Zv, Oe:globals.fo, zb:globals.HE, iG:globals.MG, Zy:globals.Qy, Rd:globals.of});
    const c = vm.createContext(globals);
    vm.runInContext(code,c);
    return c.CodexMuxAccountMenu();
  }
  assert.doesNotThrow(()=>render(injected));
  assert.doesNotThrow(()=>render(injected, true));
  assert.throws(()=>render(injected.replace(updated ? /\bZy\b/g : /\bQy\b/g,'xl')), /Invalid React element type: object/);
  console.log('PASS: installed account menu and expanded routing selector accept native component bindings.');
  console.log('PASS: negative control reproduces React invalid-element-type with the old keybinding alias.');
}
main().catch(error=>{console.error(error.message);process.exitCode=1;});
