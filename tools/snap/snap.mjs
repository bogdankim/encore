#!/usr/bin/env node
// snap: run a TUI in a real pty, mirror it into a headless xterm.js, drive it
// with keys and mouse from a JSON script, and save PNG + text screenshots.

import { createRequire } from 'node:module';
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const require = createRequire(import.meta.url);
const HERE = path.dirname(fileURLToPath(import.meta.url));
const CHROME = '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome';

const USAGE = `usage: node tools/snap/snap.mjs [options] -- <command> [args...]

  --cols N             terminal columns (default 140)
  --rows N             terminal rows (default 42)
  --cwd DIR            working directory for the command (default: current)
  --script FILE|-      steps as a JSON array, from a file or stdin
  --steps JSON         steps inline
  --out DIR            where shots go (default tools/snap/out)
  --font-size N        px (default 14)
  --font-family CSS    default '"SF Mono", "Symbols Nerd Font Mono", Menlo, monospace'
  --symbols-font FILE  Nerd Font symbols TTF (default tools/snap/fonts/SymbolsNerdFontMono-Regular.ttf)
  --line-height N      default 1.0
  --term-program NAME  TERM_PROGRAM for the command (default WezTerm)
  --env KEY=VAL        extra environment, repeatable
  --settle MS          quiet time before shot/assert (default 80)
  --no-pixels          leave the pty pixel size at 0x0
  --log FILE           write the raw pty output to FILE, plus FILE.times:
                       one line per output chunk ("<ms> out <bytes>") and
                       per step ("<ms> step <log offset> <index> <step>")
  --timeout MS         whole-run limit (default 120000)
  --chrome PATH        Chrome binary`;

// ---------- args ----------

function parseArgs(argv) {
  const o = {
    cols: 140, rows: 42, cwd: process.cwd(), script: null, steps: null,
    out: path.join(HERE, 'out'), fontSize: 14, fontFamily: '"SF Mono", "Symbols Nerd Font Mono", Menlo, monospace',
    symbolsFont: path.join(HERE, 'fonts', 'SymbolsNerdFontMono-Regular.ttf'),
    lineHeight: 1.0, termProgram: 'WezTerm', env: {}, settle: 80,
    pixels: true, log: null, timeout: 120000, chrome: CHROME, command: [],
  };
  const dd = argv.indexOf('--');
  if (dd >= 0) { o.command = argv.slice(dd + 1); argv = argv.slice(0, dd); }
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    const val = () => { if (i + 1 >= argv.length) die(`missing value for ${a}`); return argv[++i]; };
    const num = () => { const n = Number(val()); if (!Number.isFinite(n)) die(`${a} needs a number`); return n; };
    switch (a) {
      case '--cols': o.cols = num(); break;
      case '--rows': o.rows = num(); break;
      case '--cwd': o.cwd = path.resolve(val()); break;
      case '--script': o.script = val(); break;
      case '--steps': o.steps = val(); break;
      case '--out': o.out = path.resolve(val()); break;
      case '--font-size': o.fontSize = num(); break;
      case '--font-family': o.fontFamily = val(); break;
      case '--symbols-font': o.symbolsFont = path.resolve(val()); break;
      case '--line-height': o.lineHeight = num(); break;
      case '--term-program': o.termProgram = val(); break;
      case '--env': { const kv = val(); const eq = kv.indexOf('='); if (eq < 1) die(`--env wants KEY=VAL`); o.env[kv.slice(0, eq)] = kv.slice(eq + 1); break; }
      case '--settle': o.settle = num(); break;
      case '--no-pixels': o.pixels = false; break;
      case '--log': o.log = path.resolve(val()); break;
      case '--timeout': o.timeout = num(); break;
      case '--chrome': o.chrome = val(); break;
      case '-h': case '--help': console.log(USAGE); process.exit(0);
      default: die(`unknown option ${a}\n\n${USAGE}`);
    }
  }
  if (!o.command.length) die(`no command given\n\n${USAGE}`);
  return o;
}

function die(msg) { console.error(`snap: ${msg}`); process.exit(2); }

function loadSteps(o) {
  let src;
  if (o.steps != null) src = o.steps;
  else if (o.script === '-') src = fs.readFileSync(0, 'utf8');
  else if (o.script) src = fs.readFileSync(o.script, 'utf8');
  else return [{ wait: 500 }, { shot: 'screen' }];
  let steps;
  try { steps = JSON.parse(src); } catch (e) { die(`steps are not valid JSON: ${e.message}`); }
  if (steps && !Array.isArray(steps) && Array.isArray(steps.steps)) steps = steps.steps;
  if (!Array.isArray(steps)) die('steps must be a JSON array');
  return steps;
}

// ---------- keys ----------

const CSI_KEYS = { // name: [final, paramForTilde]
  up: ['A'], down: ['B'], right: ['C'], left: ['D'], home: ['H'], end: ['F'],
  insert: ['~', 2], delete: ['~', 3], pgup: ['~', 5], pageup: ['~', 5], pgdown: ['~', 6], pagedown: ['~', 6],
  f1: ['P'], f2: ['Q'], f3: ['R'], f4: ['S'],
  f5: ['~', 15], f6: ['~', 17], f7: ['~', 18], f8: ['~', 19], f9: ['~', 20], f10: ['~', 21], f11: ['~', 23], f12: ['~', 24],
};
const SIMPLE = { enter: '\r', return: '\r', esc: '\x1b', escape: '\x1b', tab: '\t', backspace: '\x7f', space: ' ' };

function encodeKey(spec, appCursor) {
  if (spec.length === 1) return spec;
  const parts = spec.split('+');
  let base = parts.pop();
  if (base === '' && spec.endsWith('+')) base = '+'; // "ctrl++"
  const mods = new Set(parts.map(p => p.toLowerCase()));
  for (const m of mods) if (!['ctrl', 'alt', 'shift', 'meta', 'opt', 'option', 'cmd'].includes(m)) throw new Error(`unknown modifier "${m}" in key "${spec}"`);
  const ctrl = mods.has('ctrl'), alt = mods.has('alt') || mods.has('meta') || mods.has('opt') || mods.has('option'), shift = mods.has('shift');
  const lname = base.toLowerCase();

  if (CSI_KEYS[lname]) {
    const [fin, num] = CSI_KEYS[lname];
    const m = 1 + (shift ? 1 : 0) + (alt ? 2 : 0) + (ctrl ? 4 : 0);
    const ss3 = 'PQRS'.includes(fin) && fin !== '~';
    if (fin === '~') return m > 1 ? `\x1b[${num};${m}~` : `\x1b[${num}~`;
    if (m > 1) return `\x1b[1;${m}${fin}`;
    if (ss3 || (appCursor && 'ABCDHF'.includes(fin))) return `\x1bO${fin}`;
    return `\x1b[${fin}`;
  }
  if (lname === 'tab' && shift) return (alt ? '\x1b' : '') + '\x1b[Z';
  let s;
  if (SIMPLE[lname] !== undefined) {
    s = SIMPLE[lname];
    if (ctrl && lname === 'space') s = '\x00';
    else if (ctrl && lname === 'backspace') s = '\x08';
  } else if ([...base].length === 1) {
    s = shift ? base.toUpperCase() : base;
    if (ctrl) {
      const c = base.toLowerCase().charCodeAt(0);
      if (c >= 97 && c <= 122) s = String.fromCharCode(c - 96);
      else if ('@[\\]^_'.includes(base)) s = String.fromCharCode(base.charCodeAt(0) - 64);
      else if (base === '?') s = '\x7f';
      else if (base === ' ') s = '\x00';
      else throw new Error(`no ctrl encoding for "${spec}"`);
    }
  } else {
    throw new Error(`unknown key "${spec}"`);
  }
  return (alt ? '\x1b' : '') + s;
}

// ---------- iTerm2 image size fix ----------

// xterm.js's image addon drops OSC 1337 File= images that have no size= field,
// while WezTerm and iTerm2 accept them. Buffer each image sequence and add the
// decoded size when it is missing, so the screenshot matches the real terminal.
const IIP_START = Buffer.from('\x1b]1337;File=', 'latin1');
const IIP_MAX = 64 * 1024 * 1024;

class IIPSizeFixer {
  constructor() { this.carry = Buffer.alloc(0); this.img = null; this.imgLen = 0; this.fixed = 0; }

  push(chunk) {
    let data = this.carry.length ? Buffer.concat([this.carry, chunk]) : chunk;
    this.carry = Buffer.alloc(0);
    const out = [];
    while (data.length) {
      if (this.img) {
        let end = -1, termLen = 1;
        for (let i = 0; i < data.length; i++) {
          if (data[i] === 0x07) { end = i; break; }
          if (data[i] === 0x1b) {
            if (i + 1 === data.length) { this.carry = data.subarray(i); data = data.subarray(0, i); break; }
            if (data[i + 1] === 0x5c) { end = i; termLen = 2; break; }
          }
        }
        if (end < 0) {
          this.img.push(data); this.imgLen += data.length;
          if (this.imgLen > IIP_MAX) { out.push(...this.img); this.img = null; }
          return out;
        }
        this.img.push(data.subarray(0, end));
        out.push(this.rewrite(Buffer.concat(this.img)), data.subarray(end, end + termLen));
        this.img = null;
        data = data.subarray(end + termLen);
        continue;
      }
      const at = data.indexOf(IIP_START);
      if (at < 0) {
        let keep = 0; // a prefix of IIP_START split across chunks
        for (let k = Math.min(IIP_START.length - 1, data.length); k > 0; k--) {
          if (data.subarray(data.length - k).equals(IIP_START.subarray(0, k))) { keep = k; break; }
        }
        out.push(data.subarray(0, data.length - keep));
        this.carry = data.subarray(data.length - keep);
        return out;
      }
      if (at > 0) out.push(data.subarray(0, at));
      this.img = []; this.imgLen = 0;
      data = data.subarray(at);
    }
    return out;
  }

  // Whatever is held back while not inside an image (called when output goes quiet).
  drain() {
    if (this.img) return Buffer.alloc(0);
    const c = this.carry; this.carry = Buffer.alloc(0); return c;
  }

  rewrite(seq) {
    const str = seq.toString('latin1');
    const colon = str.indexOf(':');
    if (colon < 0) return seq;
    const args = str.slice(IIP_START.length, colon);
    if (/(^|;)size=/.test(args)) return seq;
    const b64 = str.slice(colon + 1).replace(/\s+/g, '');
    const pad = b64.endsWith('==') ? 2 : b64.endsWith('=') ? 1 : 0;
    const size = Math.floor(b64.length * 3 / 4) - pad;
    this.fixed++;
    return Buffer.from(`${str.slice(0, IIP_START.length)}size=${size};${args}:${b64}`, 'latin1');
  }
}

// utf8Complete returns the length of buf without a trailing incomplete UTF-8
// sequence.
function utf8Complete(buf) {
  for (let i = buf.length - 1, n = 1; i >= 0 && n <= 4; i--, n++) {
    const b = buf[i];
    if ((b & 0xc0) === 0x80) continue; // continuation byte
    const need = b >= 0xf0 ? 4 : b >= 0xe0 ? 3 : b >= 0xc0 ? 2 : 1;
    return need > n ? i : buf.length;
  }
  return buf.length;
}

// ---------- main ----------

const ANSI_RE = /\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1bP[\s\S]*?\x1b\\|\x1b\[[0-?]*[ -/]*[@-~]|\x1b[@-Z\\-_]|[\x00-\x08\x0b-\x1f\x7f]/g;

async function main() {
  const t0 = Date.now();
  const o = parseArgs(process.argv.slice(2));
  const steps = loadSteps(o);
  fs.mkdirSync(o.out, { recursive: true });

  // node-pty's prebuilt spawn-helper ships without the exec bit when npm skips install scripts.
  for (const arch of ['darwin-arm64', 'darwin-x64']) {
    const h = path.join(HERE, 'node_modules/node-pty/prebuilds', arch, 'spawn-helper');
    try { const st = fs.statSync(h); if (!(st.mode & 0o111)) fs.chmodSync(h, 0o755); } catch {}
  }
  const pty = require('node-pty');
  const puppeteer = (await import('puppeteer-core')).default;

  const browser = await puppeteer.launch({
    executablePath: o.chrome,
    headless: true,
    args: ['--no-first-run', '--no-default-browser-check', '--hide-scrollbars', '--force-color-profile=srgb',
      '--font-render-hinting=none', '--allow-file-access-from-files'],
    defaultViewport: { width: 2000, height: 1400, deviceScaleFactor: 2 },
  });

  let proc = null;
  let exited = null; // {exitCode, signal}
  let rawTail = Buffer.alloc(0);
  let lastActivity = Date.now(); // last byte in either direction
  const send = (data) => { lastActivity = Date.now(); proc.write(data); };
  const shots = [];
  const failures = [];
  let geom;

  const cleanup = async () => {
    if (proc && !exited) {
      try { proc.kill('SIGTERM'); } catch {}
      const t = Date.now();
      while (!exited && Date.now() - t < 1000) await sleep(20);
      if (!exited) { try { proc.kill('SIGKILL'); } catch {} }
    }
    await browser.close().catch(() => {});
  };
  const overall = setTimeout(async () => {
    console.error(`snap: whole run exceeded ${o.timeout} ms`);
    await cleanup();
    process.exit(1);
  }, o.timeout);

  try {
    const page = await browser.newPage();
    const consoleErrors = [];
    page.on('console', m => { if (m.type() === 'error') consoleErrors.push(m.text()); });
    page.on('pageerror', e => consoleErrors.push(String(e)));

    // xterm -> pty (mouse reports, query replies)
    await page.exposeFunction('__ptyInput', (data, binary) => {
      if (!proc || exited) return;
      send(binary ? Buffer.from(data, 'latin1') : data);
    });
    await page.goto(pathToFileURL(path.join(HERE, 'page.html')).href);
    geom = await page.evaluate(opts => window.snap.init(opts), {
      cols: o.cols, rows: o.rows, fontSize: o.fontSize, fontFamily: o.fontFamily,
      lineHeight: o.lineHeight,
      symbolsFont: fs.existsSync(o.symbolsFont) ? pathToFileURL(o.symbolsFont).href : null,
    });
    if (!geom.symbolsOk) console.error(`snap: warning: no Nerd Font symbols (${o.symbolsFont}), icons will show as boxes`);
    if (!geom.fontOk) console.error(`snap: warning: font ${o.fontFamily} did not load, using a fallback`);
    await page.setViewport({ width: Math.ceil(geom.width) + 2, height: Math.ceil(geom.height) + 2, deviceScaleFactor: 2 });

    // pty -> xterm, strictly in order
    let chain = Promise.resolve();
    const iip = new IIPSizeFixer();
    let pending = [];
    let scheduled = false;
    // xterm.js drops a UTF-8 character whose last byte arrives in a later
    // write() than its first two ("…" split as e2 80 | a6 vanishes and the
    // rest of the row shifts left), so hold an incomplete tail back until the
    // next batch. sync() sends it anyway when the output goes quiet.
    let utf8Tail = Buffer.alloc(0);
    const flushPending = (final = false) => {
      scheduled = false;
      if (utf8Tail.length) { pending.unshift(utf8Tail); utf8Tail = Buffer.alloc(0); }
      if (!pending.length) return;
      let buf = Buffer.concat(pending); pending = [];
      const cut = final ? buf.length : utf8Complete(buf);
      if (cut < buf.length) { utf8Tail = buf.subarray(cut); buf = buf.subarray(0, cut); }
      if (!buf.length) return;
      chain = chain.then(() => page.evaluate(b => window.snap.write(b), buf.toString('base64'))).catch(() => {});
    };
    const sync = async () => { const held = iip.drain(); if (held.length) pending.push(held); flushPending(true); await chain; await page.evaluate(() => window.snap.flush()); };
    const settle = async (maxMs = 1500) => {
      const start = Date.now();
      while (Date.now() - lastActivity < o.settle && Date.now() - start < maxMs) await sleep(10);
      await sync();
    };

    // Sets cols, rows and pixel size in one TIOCSWINSZ; false if that failed.
    const setPixels = (cols, rows) => {
      const xp = Math.round(geom.cellW * cols), yp = Math.round(geom.cellH * rows);
      try {
        execFileSync('/usr/bin/python3', ['-c',
          'import sys,os,fcntl,termios,struct\nfd=os.open(sys.argv[1],os.O_RDWR|os.O_NOCTTY)\nfcntl.ioctl(fd,termios.TIOCSWINSZ,struct.pack("HHHH",*map(int,sys.argv[2:])))',
          proc.ptsName ?? proc._pty, String(rows), String(cols), String(xp), String(yp)], { stdio: 'ignore' });
        return true;
      } catch (e) {
        if (!setPixels.warned) { console.error(`snap: could not set pty pixel size (${e.message.split('\n')[0]})`); setPixels.warned = true; }
        return false;
      }
    };

    // The command starts behind a tiny python exec shim so the pixel size is
    // already in the winsize when it first asks.
    const env = {
      ...process.env, TERM: 'xterm-256color', COLORTERM: 'truecolor', TERM_PROGRAM: o.termProgram,
      ...o.env,
    };
    delete env.TERM_PROGRAM_VERSION; delete env.TMUX; delete env.ITERM_SESSION_ID; delete env.KITTY_WINDOW_ID;
    delete env.WEZTERM_PANE; delete env.WEZTERM_EXECUTABLE; delete env.NO_COLOR;
    let file = resolveCommand(o.command[0], o.cwd, env.PATH), args = o.command.slice(1);
    if (o.pixels && fs.existsSync('/usr/bin/python3')) {
      const xp = Math.round(geom.cellW * o.cols), yp = Math.round(geom.cellH * o.rows);
      args = ['-c', 'import sys,os,fcntl,termios,struct\ntry: fcntl.ioctl(0,termios.TIOCSWINSZ,struct.pack("HHHH",*map(int,sys.argv[1:5])))\nexcept Exception: pass\nos.execvp(sys.argv[5],sys.argv[5:])',
        String(o.rows), String(o.cols), String(xp), String(yp), file, ...args];
      file = '/usr/bin/python3';
    }
    const logFd = o.log ? fs.openSync(o.log, 'w') : null;
    const timesFd = o.log ? fs.openSync(o.log + '.times', 'w') : null;
    let logged = 0;
    const logTime = line => { if (timesFd !== null) fs.writeSync(timesFd, `${Date.now() - t0} ${line}\n`); };
    proc = pty.spawn(file, args, { name: 'xterm-256color', cols: o.cols, rows: o.rows, cwd: o.cwd, env, encoding: null });
    proc.onData(d => {
      const b = Buffer.isBuffer(d) ? d : Buffer.from(d);
      lastActivity = Date.now();
      if (logFd !== null) { fs.writeSync(logFd, b); logTime(`out ${b.length}`); logged += b.length; }
      rawTail = Buffer.concat([rawTail, b]); if (rawTail.length > 65536) rawTail = rawTail.subarray(rawTail.length - 65536);
      for (const part of iip.push(b)) if (part.length) pending.push(part);
      if (!scheduled) { scheduled = true; setImmediate(flushPending); }
    });
    proc.onExit(e => { exited = e; });

    const cellXY = ([c, r]) => {
      if (!Number.isInteger(c) || !Number.isInteger(r)) throw new Error(`cell must be [col,row] integers, got ${JSON.stringify([c, r])}`);
      if (c < 0 || r < 0 || c >= geom.cols || r >= geom.rows) throw new Error(`cell [${c},${r}] is outside ${geom.cols}x${geom.rows}`);
      return { x: geom.x + (c + 0.5) * geom.cellW, y: geom.y + (r + 0.5) * geom.cellH };
    };
    const screenText = () => page.evaluate(() => window.snap.text());

    // region: [col, row, cols, rows] crops both the PNG and the text to those cells.
    const shot = async (name, region, scale = 2) => {
      await sync();
      const png = path.join(o.out, `${name}.png`), txt = path.join(o.out, `${name}.txt`);
      fs.mkdirSync(path.dirname(png), { recursive: true });
      let clip = { x: geom.x, y: geom.y, width: geom.width, height: geom.height };
      let text = await screenText();
      if (region) {
        const [c, r, w, h] = region;
        if (![c, r, w, h].every(Number.isInteger) || c < 0 || r < 0 || w < 1 || h < 1 || c + w > geom.cols || r + h > geom.rows) {
          throw new Error(`region ${JSON.stringify(region)} must be [col,row,cols,rows] inside ${geom.cols}x${geom.rows}`);
        }
        clip = { x: geom.x + c * geom.cellW, y: geom.y + r * geom.cellH, width: w * geom.cellW, height: h * geom.cellH };
        text = await page.evaluate((c, r, w, h) => window.snap.text(c, r, w, h), c, r, w, h);
      }
      // scale > 2 zooms in: the page renders at devicePixelRatio 2, the clip scale adds on top
      await page.screenshot({ path: png, clip: { ...clip, scale: scale / 2 } });
      fs.writeFileSync(txt, text + '\n');
      shots.push(png);
      return png;
    };

    const runStep = async (step, i) => {
      const kind = Object.keys(step).find(k => ['wait', 'waitFor', 'keys', 'key', 'type', 'click', 'move', 'drag', 'release', 'wheel', 'resize', 'shot', 'assert', 'assertNot', 'expectExit'].includes(k));
      if (!kind) throw new Error(`step ${i}: unknown step ${JSON.stringify(step)}`);
      if (exited && kind !== 'expectExit' && kind !== 'shot' && kind !== 'wait') throw new Error(`process exited (code ${exited.exitCode}, signal ${exited.signal}) before step ${i} ${JSON.stringify(step)}`);
      switch (kind) {
        case 'wait': await sleep(step.wait); break;
        case 'waitFor': {
          const timeout = step.timeout ?? 5000, start = Date.now();
          for (;;) {
            await sync();
            if ((await screenText()).includes(step.waitFor)) break;
            if (exited) throw new Error(`process exited while waiting for ${JSON.stringify(step.waitFor)}`);
            if (Date.now() - start > timeout) throw new Error(`timed out after ${timeout} ms waiting for ${JSON.stringify(step.waitFor)}`);
            await sleep(25);
          }
          break;
        }
        case 'key': case 'keys': {
          const list = Array.isArray(step[kind]) ? step[kind] : [step[kind]];
          const delay = step.delay ?? 40;
          for (let k = 0; k < list.length; k++) {
            const { appCursor } = await page.evaluate(() => window.snap.modes());
            const seq = encodeKey(String(list[k]), appCursor);
            send(seq);
            // A lone ESC (or ESC+char) is held by Bubble Tea for 50 ms in case more follows.
            const pause = seq[0] === '\x1b' && seq.length <= 2 && k < list.length - 1 ? Math.max(delay, 90) : delay;
            if (pause) await sleep(pause);
          }
          break;
        }
        case 'type': send(String(step.type)); if (step.delay) await sleep(step.delay); break;
        case 'click': {
          const { x, y } = cellXY(step.click);
          const button = step.button ?? 'left';
          if (!['left', 'right', 'middle'].includes(button)) throw new Error(`step ${i}: button must be left|right|middle`);
          await page.mouse.move(x, y);
          if (step.double) await page.mouse.click(x, y, { button, count: 2 });
          else await page.mouse.click(x, y, { button });
          break;
        }
        case 'move': { const { x, y } = cellXY(step.move); await page.mouse.move(x, y); break; }
        case 'drag': {
          const a = cellXY(step.drag.from), b = cellXY(step.drag.to);
          const button = step.button ?? 'left';
          const n = Math.max(1, Math.abs(step.drag.to[0] - step.drag.from[0]), Math.abs(step.drag.to[1] - step.drag.from[1]));
          await page.mouse.move(a.x, a.y);
          await page.mouse.down({ button });
          for (let s = 1; s <= n; s++) { await page.mouse.move(a.x + (b.x - a.x) * s / n, a.y + (b.y - a.y) * s / n); await sleep(8); }
          if (!step.hold) await page.mouse.up({ button });
          break;
        }
        case 'release': {
          const { x, y } = cellXY(step.release);
          await page.mouse.move(x, y);
          await page.mouse.up({ button: step.button ?? 'left' });
          break;
        }
        case 'wheel': {
          const { x, y } = cellXY(step.wheel);
          const dy = step.dy ?? 1, dx = step.dx ?? 0;
          await page.mouse.move(x, y);
          for (let s = 0; s < Math.abs(dy); s++) { await page.mouse.wheel({ deltaY: Math.sign(dy) * 100 }); await sleep(10); }
          for (let s = 0; s < Math.abs(dx); s++) { await page.mouse.wheel({ deltaX: Math.sign(dx) * 100 }); await sleep(10); }
          break;
        }
        case 'resize': {
          const [c, r] = step.resize;
          geom = await page.evaluate((c, r) => window.snap.resize(c, r), c, r);
          await page.setViewport({ width: Math.ceil(geom.width) + 2, height: Math.ceil(geom.height) + 2, deviceScaleFactor: 2 });
          geom = await page.evaluate(() => window.snap.geometry());
          if (!(o.pixels && setPixels(c, r))) proc.resize(c, r);
          break;
        }
        case 'shot': await settle(); await shot(step.shot, step.region, step.scale ?? 2); break;
        case 'assert': case 'assertNot': {
          const want = step[kind];
          const timeout = step.timeout ?? 1000, start = Date.now();
          await settle();
          for (;;) {
            const has = (await screenText()).includes(want);
            if (has === (kind === 'assert')) break;
            if (Date.now() - start >= timeout) throw new Error(kind === 'assert' ? `expected screen to contain ${JSON.stringify(want)}` : `expected screen NOT to contain ${JSON.stringify(want)}`);
            await sleep(25); await sync();
          }
          break;
        }
        case 'expectExit': {
          const timeout = step.expectExit || 3000, start = Date.now();
          while (!exited && Date.now() - start < timeout) await sleep(10);
          if (!exited) throw new Error(`process still running ${timeout} ms after expectExit`);
          if (step.code !== undefined && exited.exitCode !== step.code) throw new Error(`process exited with ${exited.exitCode}, expected ${step.code}`);
          break;
        }
      }
      // Input just went out (mouse reports arrive via an async binding), so the
      // next settle measures quiet time from here.
      if (['keys', 'key', 'type', 'click', 'move', 'drag', 'release', 'wheel', 'resize'].includes(kind)) lastActivity = Date.now();
    };

    for (let i = 0; i < steps.length; i++) {
      try {
        logTime(`step ${logged} ${i} ${JSON.stringify(steps[i])}`);
        await runStep(steps[i], i);
      } catch (e) {
        const name = `fail-step${i}`;
        await settle(300).catch(() => {});
        await shot(name).catch(() => {});
        failures.push(`step ${i} ${JSON.stringify(steps[i])}: ${e.message}`);
        break;
      }
    }

    // Did the process die on its own?
    await sleep(30);
    const expected = steps.some(s => 'expectExit' in s);
    if (exited && !expected) {
      if (!failures.length) failures.push(`process exited early (code ${exited.exitCode}, signal ${exited.signal})`);
    }
    if (exited && (!expected || failures.length)) {
      await settle(300).catch(() => {});
      await shot('exit').catch(() => {});
      const tail = rawTail.toString('utf8').replace(ANSI_RE, '').split(/\r?\n/).map(l => l.replace(/\r/g, '').trimEnd()).filter(Boolean).slice(-25);
      console.error(`--- output tail ---\n${tail.join('\n')}\n-------------------`);
    }
    if (consoleErrors.length) console.error(`snap: page errors:\n  ${consoleErrors.slice(0, 10).join('\n  ')}`);
  } catch (e) {
    failures.push(`harness error: ${e.stack || e.message}`);
  } finally {
    clearTimeout(overall);
    await cleanup();
  }

  const secs = ((Date.now() - t0) / 1000).toFixed(1);
  const rel = p => { const r = path.relative(process.cwd(), p); return r && !r.startsWith('..') ? r : p; };
  for (const p of shots) console.log(`shot ${rel(p)}  ${rel(p.replace(/\.png$/, '.txt'))}`);
  if (failures.length) {
    for (const f of failures) console.error(`FAIL ${f}`);
    console.error(`snap: failed after ${secs}s (${geom ? `${geom.cols}x${geom.rows}` : '?'})`);
    process.exit(1);
  }
  console.log(`snap: ok, ${steps.length} steps, ${shots.length} shots, ${secs}s`);
  process.exit(0);
}

function resolveCommand(cmd, cwd, pathVar = '') {
  const ok = p => { try { fs.accessSync(p, fs.constants.X_OK); return fs.statSync(p).isFile(); } catch { return false; } };
  if (cmd.includes('/')) {
    const p = path.resolve(cwd, cmd);
    if (!ok(p)) die(`command not found or not executable: ${p}`);
    return p;
  }
  for (const dir of pathVar.split(':')) if (dir && ok(path.join(dir, cmd))) return path.join(dir, cmd);
  die(`command not found in PATH: ${cmd}`);
}

function sleep(ms) { return new Promise(r => setTimeout(r, ms)); }

main().catch(e => { console.error(e); process.exit(1); });
