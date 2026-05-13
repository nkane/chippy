// chippy.js — JS shell driving the chippy WASM module.
//
// Boot sequence:
//   1. wasm_exec.js exposes the `Go` runtime.
//   2. Fetch chippy.wasm and instantiate; Go runtime starts, installing
//      a `chippy` global with step/run/state/load/etc.
//   3. Wire DOM events to that global.

const status = (msg) => {
  document.getElementById('status').textContent = msg;
};

const wasmStatus = (msg) => {
  document.getElementById('wasm-status').textContent = msg;
};

// Demos are tiny pre-canned ROMs served from web/demos/.
const DEMOS = [
  { name: 'count to ten', file: 'demos/count_to_ten.bin', format: 'bin', addr: 0x8000 },
  { name: 'fibonacci', file: 'demos/fibonacci.bin', format: 'bin', addr: 0x8000 },
  { name: 'hello (Apple-1 style)', file: 'demos/hello.bin', format: 'bin', addr: 0x8000 },
];

function fmtHex(n, width) {
  return '$' + n.toString(16).toUpperCase().padStart(width, '0');
}

function flagBits(p) {
  return [
    p & 0x80 ? 'N' : '.',
    p & 0x40 ? 'V' : '.',
    '-',
    p & 0x10 ? 'B' : '.',
    p & 0x08 ? 'D' : '.',
    p & 0x04 ? 'I' : '.',
    p & 0x02 ? 'Z' : '.',
    p & 0x01 ? 'C' : '.',
  ].join('');
}

function renderState() {
  if (!window.chippy) return;
  const s = window.chippy.state();
  document.getElementById('reg-a').textContent = fmtHex(s.a, 2);
  document.getElementById('reg-x').textContent = fmtHex(s.x, 2);
  document.getElementById('reg-y').textContent = fmtHex(s.y, 2);
  document.getElementById('reg-sp').textContent = fmtHex(s.sp, 2);
  document.getElementById('reg-pc').textContent = fmtHex(s.pc, 4);
  document.getElementById('reg-cyc').textContent = s.cycles.toString();
  // P pane renders as three rows in a monospace <pre>: hex value,
  // legend, then live flag state — each row 8 chars wide so the
  // letters line up under their bit positions.
  document.getElementById('reg-p').textContent =
    fmtHex(s.p, 2) + '\nNV-BDIZC\n' + flagBits(s.p);

  renderDisasm(s.pc);
  renderMemory();
  document.getElementById('text-out').textContent = window.chippy.textOutput();
  status(s.halted ? 'halted' : `pc=${fmtHex(s.pc, 4)} cyc=${s.cycles}`);
}

function renderDisasm(pc) {
  const rows = window.chippy.disasm(pc, 16);
  const lines = [];
  for (const r of rows) {
    const bytes = Array.from(r.bytes).map((b) => b.toString(16).toUpperCase().padStart(2, '0')).join(' ');
    const isPC = r.addr === pc ? '>' : ' ';
    lines.push(`${isPC} ${fmtHex(r.addr, 4)}: ${bytes.padEnd(10)} ${r.text}`);
  }
  document.getElementById('disasm-view').textContent = lines.join('\n');
}

function renderMemory() {
  const addrInput = document.getElementById('mem-addr').value.trim();
  const addr = parseAddr(addrInput) & 0xFFF0; // align to 16-byte row
  const bytes = window.chippy.readMem(addr, 256);
  const lines = [];
  for (let row = 0; row < 16; row++) {
    const base = (addr + row * 16) & 0xFFFF;
    const hex = [];
    const ascii = [];
    for (let i = 0; i < 16; i++) {
      const b = bytes[row * 16 + i];
      hex.push(b.toString(16).toUpperCase().padStart(2, '0'));
      ascii.push(b >= 0x20 && b < 0x7F ? String.fromCharCode(b) : '.');
    }
    lines.push(`${fmtHex(base, 4)}  ${hex.slice(0, 8).join(' ')}  ${hex.slice(8).join(' ')}  ${ascii.join('')}`);
  }
  document.getElementById('mem-view').textContent = lines.join('\n');
}

function parseAddr(s) {
  s = s.trim().toLowerCase();
  if (s.startsWith('$')) return parseInt(s.slice(1), 16) & 0xFFFF;
  if (s.startsWith('0x')) return parseInt(s.slice(2), 16) & 0xFFFF;
  return parseInt(s, 16) & 0xFFFF;
}

async function loadDemo() {
  const sel = document.getElementById('demo');
  const demo = DEMOS[sel.selectedIndex];
  try {
    const r = await fetch(demo.file);
    if (!r.ok) throw new Error(`fetch ${demo.file}: ${r.status}`);
    const bytes = new Uint8Array(await r.arrayBuffer());
    const opts = { format: demo.format, addr: demo.addr };
    const result = window.chippy.load(bytes, opts);
    if (!result.ok) {
      status('load failed: ' + result.error);
      return;
    }
    lastLoaded = { bytes, opts: { ...opts, variant: document.getElementById('variant').value } };
    status(`loaded ${demo.name} (${result.format}, $${result.loadAddr.toString(16).toUpperCase()}, ${result.size}B)`);
    renderState();
  } catch (err) {
    status('demo load error: ' + err.message);
  }
}

async function loadUserFile(file) {
  const buf = new Uint8Array(await file.arrayBuffer());
  const fmt = document.getElementById('format').value;
  const addr = parseAddr(document.getElementById('addr').value);
  const opts = { format: fmt, addr };
  const result = window.chippy.load(buf, opts);
  if (!result.ok) {
    status('load failed: ' + result.error);
    return;
  }
  lastLoaded = { bytes: buf, opts: { ...opts, variant: document.getElementById('variant').value } };
  status(`loaded ${file.name} (${result.format}, $${result.loadAddr.toString(16).toUpperCase()}, ${result.size}B)`);
  renderState();
}

function step() {
  window.chippy.step();
  renderState();
}

function runN(n) {
  const r = window.chippy.run(n);
  status(r.halted ? `halted after ${r.steps} steps` : `ran ${r.steps} steps`);
  renderState();
}

function resetCPU() {
  window.chippy.reset();
  renderState();
}

function variantChanged() {
  const v = document.getElementById('variant').value;
  window.chippy.setVariant(v);
  status(`variant -> ${v} (load a ROM to run)`);
  renderState();
}

function bindKeyCapture() {
  const el = document.getElementById('key-capture');
  el.addEventListener('keydown', (ev) => {
    let b;
    if (ev.key.length === 1) {
      b = ev.key.charCodeAt(0);
    } else if (ev.key === 'Enter') {
      b = 0x0D;
    } else if (ev.key === 'Backspace') {
      b = 0x08;
    } else if (ev.key === 'Escape') {
      b = 0x1B;
    } else {
      return;
    }
    window.chippy.pushKey(b);
    ev.preventDefault();
    status(`pushed key $${b.toString(16).toUpperCase().padStart(2, '0')}`);
  });
}

function bindUI() {
  const demoSel = document.getElementById('demo');
  for (const d of DEMOS) {
    const opt = document.createElement('option');
    opt.textContent = d.name;
    demoSel.appendChild(opt);
  }

  document.getElementById('load-demo').addEventListener('click', loadDemo);
  document.getElementById('file').addEventListener('change', (ev) => {
    const f = ev.target.files[0];
    if (f) loadUserFile(f);
  });
  document.getElementById('reset').addEventListener('click', resetCPU);
  document.getElementById('step').addEventListener('click', step);
  document.getElementById('run100').addEventListener('click', () => runN(100));
  document.getElementById('run10k').addEventListener('click', () => runN(10_000));
  document.getElementById('clear-text').addEventListener('click', () => {
    window.chippy.clearTextOutput();
    renderState();
  });
  document.getElementById('variant').addEventListener('change', variantChanged);
  document.getElementById('mem-addr').addEventListener('change', renderState);
  document.getElementById('share').addEventListener('click', copyShareLink);

  // Keyboard shortcut: lower-case "s" steps when not in an input.
  document.addEventListener('keydown', (ev) => {
    if (ev.target.tagName === 'INPUT' || ev.target.tagName === 'SELECT' || ev.target.id === 'key-capture') return;
    if (ev.key === 's') step();
  });

  bindKeyCapture();
}

async function boot() {
  wasmStatus('fetching chippy.wasm…');
  const go = new Go();
  const result = await WebAssembly.instantiateStreaming(fetch('chippy.wasm'), go.importObject)
    .catch(async () => {
      // Some servers don't set application/wasm; fall back to arrayBuffer path.
      const resp = await fetch('chippy.wasm');
      const buf = await resp.arrayBuffer();
      return WebAssembly.instantiate(buf, go.importObject);
    });
  go.run(result.instance);
  wasmStatus('chippy.wasm ready');
  bindUI();
  await maybeLoadFromHash();
  renderState();
  registerServiceWorker(); // non-blocking; offline-ready on repeat visits
}

// Share permalink: encode the loaded ROM + variant + load addr + format
// into the URL fragment. Fragment stays client-side so we don't ship
// the bytes to any server.
function buildShareLink(opts, bytes) {
  const params = new URLSearchParams();
  params.set('format', opts.format);
  if (opts.addr) params.set('addr', String(opts.addr));
  if (opts.variant) params.set('variant', opts.variant);
  if (bytes && bytes.length) {
    params.set('rom', base64Encode(bytes));
  }
  return location.origin + location.pathname + '#' + params.toString();
}

let lastLoaded = null; // {bytes, opts} so share can rebuild the link.

function copyShareLink() {
  if (!lastLoaded) {
    status('share: load a ROM first');
    return;
  }
  const link = buildShareLink(lastLoaded.opts, lastLoaded.bytes);
  if (navigator.clipboard) {
    navigator.clipboard.writeText(link).then(
      () => status('share link copied to clipboard'),
      () => fallbackShare(link),
    );
    return;
  }
  fallbackShare(link);
}

function fallbackShare(link) {
  // Clipboard API unavailable (insecure context, old browsers). Stuff
  // the link into the URL bar so user can copy from the address bar.
  history.replaceState(null, '', link);
  status('share link in URL bar');
}

function base64Encode(bytes) {
  // chunk to avoid String.fromCharCode call-stack limits on large ROMs
  let s = '';
  const CHUNK = 0x8000;
  for (let i = 0; i < bytes.length; i += CHUNK) {
    s += String.fromCharCode.apply(null, bytes.subarray(i, i + CHUNK));
  }
  return btoa(s);
}

function base64Decode(s) {
  const bin = atob(s);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

async function maybeLoadFromHash() {
  if (!location.hash || location.hash.length < 2) return;
  const params = new URLSearchParams(location.hash.slice(1));
  const romB64 = params.get('rom');
  if (!romB64) return;
  let bytes;
  try {
    bytes = base64Decode(romB64);
  } catch (err) {
    status('share link: bad base64 (' + err.message + ')');
    return;
  }
  const format = params.get('format') || 'bin';
  const addr = params.get('addr') ? parseAddr(params.get('addr')) : 0x8000;
  const variant = params.get('variant') || 'nmos';
  document.getElementById('variant').value = variant;
  window.chippy.setVariant(variant);
  const opts = { format, addr };
  const result = window.chippy.load(bytes, opts);
  if (!result.ok) {
    status('share-link load failed: ' + result.error);
    return;
  }
  lastLoaded = { bytes, opts: { ...opts, variant } };
  status(`loaded from share link (${result.format}, $${result.loadAddr.toString(16).toUpperCase()}, ${result.size}B)`);
}

function registerServiceWorker() {
  if (!('serviceWorker' in navigator)) return;
  navigator.serviceWorker.register('sw.js').catch((err) => {
    console.warn('service worker registration failed:', err);
  });
}

function showBootError(msg) {
  const node = document.getElementById('boot-error');
  if (!node) {
    wasmStatus(msg);
    return;
  }
  node.textContent = msg;
  node.hidden = false;
  wasmStatus('boot failed — see banner above');
}

boot().catch((err) => {
  console.error(err);
  showBootError(
    'Could not load chippy.wasm.\n\n' +
    err.message + '\n\n' +
    'If you are running from a file:// URL, browsers refuse to ' +
    'instantiate WASM there. Use `make -C web serve` (or any static ' +
    'HTTP server) and reload from http://localhost:8080/.'
  );
});
