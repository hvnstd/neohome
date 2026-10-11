package webtty

import "strings"

// ---------------------------------------------------------------------------
// The front end
//
// The page is served by the world itself and carries no external asset: no
// CDN, no font, no image, no framework. A world whose terminal needed the
// internet to render would be a world that stops working offline, and offline
// is a state this game is about.
//
// The terminal engine (TermJS) has no DOM in it: it turns the server's text
// stream into rows of styled runs. That makes it testable — the test harness
// runs it under node with a stub and asserts what the screen would show — and
// it keeps the browser half (PageJS) to key handling and rendering.
// ---------------------------------------------------------------------------

// TermJS is the terminal engine: stream in, screen out. It is exported so the
// tests can run the exact bytes the page ships under node, with no browser.
func TermJS() string { return termJS }

// PageJS is the browser half: login, WebSocket, keys, rendering.
func PageJS() string { return pageJS }

// Page is the whole front end as one self-contained document.
func Page(title string) string {
	return strings.NewReplacer(
		"{{TITLE}}", title,
		"{{TERM_JS}}", termJS,
		"{{PAGE_JS}}", pageJS,
	).Replace(pageHTML)
}

// pageHTML is the document. Styles are inline on purpose: one request, no
// external anything, and no CSP exception to argue about.
const pageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{TITLE}}</title>
<style>
:root { color-scheme: dark; }
* { box-sizing: border-box; }
html, body { margin: 0; height: 100%; background: #0b0e10; color: #cfd8dc;
  font: 14px/1.45 ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace; }
#app { display: flex; flex-direction: column; height: 100%; }
header { display: flex; align-items: center; gap: 12px; padding: 8px 12px;
  background: #12171b; border-bottom: 1px solid #223; font-size: 12px; }
header .brand { color: #7fd1a8; font-weight: 600; letter-spacing: .04em; }
header .who { color: #8fa3ad; }
header .spacer { flex: 1; }
header .state { padding: 1px 8px; border-radius: 10px; background: #1d2a22; color: #7fd1a8; }
header .state.bad { background: #2c1a1a; color: #e08a8a; }
button { font: inherit; font-size: 12px; color: #cfd8dc; background: #1a2227;
  border: 1px solid #2b3941; border-radius: 4px; padding: 3px 10px; cursor: pointer; }
button:hover { background: #22303a; }
#screen { position: relative; flex: 1; overflow: hidden; padding: 10px 12px; }
#rows { white-space: pre; overflow: hidden; }
#rows .b { font-weight: 700; }
#rows .d { opacity: .6; }
#rows .r { background: #cfd8dc; color: #0b0e10; }
#rows .c31, #rows .C91 { color: #e06c75; }
#rows .c32, #rows .C92 { color: #98c379; }
#rows .c33, #rows .C93 { color: #e5c07b; }
#rows .c34, #rows .C94 { color: #61afef; }
#rows .c35, #rows .C95 { color: #c678dd; }
#rows .c36, #rows .C96 { color: #56b6c2; }
#rows .c37, #rows .C97 { color: #dcdfe4; }
#rows .c30, #rows .C90 { color: #5c6370; }
#cursor { display: inline-block; width: .6em; height: 1em; vertical-align: -.15em;
  background: #7fd1a8; animation: blink 1.1s steps(1) infinite; }
@keyframes blink { 50% { opacity: 0 } }
#login { position: absolute; inset: 0; display: flex; align-items: center;
  justify-content: center; background: #0b0e10ee; }
#login form { background: #12171b; border: 1px solid #223; border-radius: 6px;
  padding: 22px 24px; width: 320px; }
#login h1 { margin: 0 0 4px; font-size: 16px; color: #7fd1a8; }
#login p { margin: 0 0 16px; font-size: 12px; color: #8fa3ad; }
#login label { display: block; font-size: 12px; color: #8fa3ad; margin-bottom: 4px; }
#login input { width: 100%; margin-bottom: 12px; padding: 7px 9px; font: inherit;
  color: #cfd8dc; background: #0b0e10; border: 1px solid #2b3941; border-radius: 4px; }
#login input:focus { outline: 1px solid #7fd1a8; }
#login .err { min-height: 16px; margin: 8px 0 0; font-size: 12px; color: #e08a8a; }
#login button { width: 100%; padding: 8px; }
footer { padding: 4px 12px; font-size: 11px; color: #5c6b73; border-top: 1px solid #1a2227; }
#hidden { position: absolute; left: -9999px; top: 0; width: 1px; height: 1px; }
</style>
</head>
<body>
<div id="app">
  <header>
    <span class="brand">NEOHOME</span>
    <span class="who" id="who"></span>
    <span class="spacer"></span>
    <span class="state" id="state">connecting</span>
    <button id="disc" type="button">disconnect</button>
  </header>
  <div id="screen">
    <div id="rows"></div>
    <textarea id="hidden" autocomplete="off" autocapitalize="off" autocorrect="off" spellcheck="false" aria-label="terminal input"></textarea>
    <div id="login">
      <form id="loginform" autocomplete="off">
        <h1>{{TITLE}}</h1>
        <p>Sign in with your world account. This is a real session: it shows up in <b>who</b> and in the machine's own log.</p>
        <label for="u">account</label>
        <input id="u" name="user" value="alex" autocapitalize="off" autocorrect="off" spellcheck="false">
        <label for="p">password</label>
        <input id="p" name="pass" type="password">
        <div class="err" id="err"></div>
        <button type="submit" id="go">open terminal</button>
      </form>
    </div>
  </div>
  <footer id="foot">keys are forwarded as typed; the machine's shell echoes them back. ^L clears, ^C cancels the line, arrow up recalls.</footer>
</div>
<script>{{TERM_JS}}</script>
<script>{{PAGE_JS}}</script>
</body>
</html>
`

// termJS is the terminal engine. Nothing in it touches the DOM, so it runs
// unchanged under node for the tests.
const termJS = `
function NeohomeTerm(cols, rows) {
  this.cols = cols || 80;
  this.rows = rows || 24;
  this.done = [];            // finished lines, each: [{t:"text", s:"classes"}]
  this.line = [];            // the line being built
  this.col = 0;              // cursor column, in cells
  this.style = "";           // active SGR classes
  this.secret = false;       // a password prompt is on screen
  this.masked = 0;           // characters typed while secret
  this.pending = "";         // an incomplete escape sequence
  this.cr = false;           // the last thing written was a bare CR
  this.maxDone = 2000;
}
NeohomeTerm.prototype.push = function (text, cls) {
  if (text === "") return;
  if (this.cr) { this.overwrite(text, cls); return; }
  var last = this.line[this.line.length - 1];
  if (last && last.s === cls) { last.t += text; }
  else { this.line.push({ t: text, s: cls }); }
  this.col += text.length;
};
// overwrite writes over the cells from the cursor rightwards and keeps
// whatever lay beyond the new text: that is how a progress line rewrites
// itself in place, and why a shorter write does not erase the rest of a line.
NeohomeTerm.prototype.overwrite = function (text, cls) {
  var len = this.lineText().length;
  var start = Math.max(0, Math.min(this.col, len));
  var head = this.styleRuns(0, start);
  var tail = this.styleRuns(start + text.length, len);
  head.push({ t: text, s: cls });
  this.line = head.concat(tail);
  this.col = start + text.length;
  // cr stays set: the rest of the line is written the same way, one character
  // at a time, until something moves the cursor off the line
};
// styleRuns cuts [from,to) out of the current line, keeping each run's style.
NeohomeTerm.prototype.styleRuns = function (from, to) {
  if (to <= from) return [];
  var out = [], pos = 0;
  for (var i = 0; i < this.line.length; i++) {
    var r = this.line[i], end = pos + r.t.length;
    if (end > from && pos < to) {
      out.push({ t: r.t.slice(Math.max(from, pos) - pos, Math.min(to, end) - pos), s: r.s });
    }
    pos = end;
  }
  return out;
};
NeohomeTerm.prototype.lineText = function () {
  var out = "";
  for (var i = 0; i < this.line.length; i++) out += this.line[i].t;
  return out;
};
NeohomeTerm.prototype.newline = function () {
  this.cr = false;
  this.done.push(this.line);
  if (this.done.length > this.maxDone) this.done.splice(0, this.done.length - this.maxDone);
  this.line = [];
  this.col = 0;
};
NeohomeTerm.prototype.carriageReturn = function () {
  // a bare CR returns to column 0 without clearing: the next write overwrites
  // from there, which is how a progress line rewrites itself in place
  this.col = 0;
  this.cr = true;
};
NeohomeTerm.prototype.backspace = function () {
  this.cr = false;
  var text = this.lineText();
  if (text.length === 0) return;
  text = text.slice(0, -1);
  this.line = text ? [{ t: text, s: this.style }] : [];
  if (this.col > 0) this.col--;
};
NeohomeTerm.prototype.eraseInLine = function () {
  this.cr = false;
  this.line = [];
  this.col = 0;
};
NeohomeTerm.prototype.clear = function () {
  this.cr = false;
  this.done = [];
  this.line = [];
  this.col = 0;
};
NeohomeTerm.prototype.setSecret = function (on) {
  this.secret = !!on;
  this.masked = 0;
};
NeohomeTerm.prototype.echo = function (ch) {
  // the masked echo of a password: the shell stays silent, the screen does not
  if (this.secret) { this.masked += 1; this.push("\u2022", this.style); return; }
  this.push(ch, this.style);
};
// secretInput is the client's half of a password prompt: the bytes go to the
// shell untouched, and the screen shows one bullet per character typed. The
// world's shell echoes nothing at a prompt (it never does, on any door), so
// this is the only sign the keyboard is landing — and it is a display choice
// only: the bullets are never sent anywhere.
NeohomeTerm.prototype.secretInput = function (ch) {
  if (!this.secret || !ch) return;
  for (var i = 0; i < ch.length; i++) {
    var c = ch.charAt(i);
    if (c === "\u007f" || c === "\b") {
      if (this.masked > 0) { this.masked -= 1; this.backspace(); }
      continue;
    }
    if (c === "\r" || c === "\n") { this.cr = false; continue; }
    if (c === "\u0003" || c === "\u0004") { this.masked = 0; continue; }
    if (c < " ") continue;
    this.masked += 1;
    this.push("\u2022", this.style);
  }
};
NeohomeTerm.prototype.applySGR = function (params) {
  var set = {};
  var codes = params.split(";");
  if (codes.length === 0 || (codes.length === 1 && codes[0] === "")) codes = ["0"];
  for (var i = 0; i < codes.length; i++) {
    var n = parseInt(codes[i] === "" ? "0" : codes[i], 10);
    if (isNaN(n)) continue;
    if (n === 0) { set = {}; }
    else if (n === 1) set.b = 1;
    else if (n === 2) set.d = 1;
    else if (n === 7) set.r = 1;
    else if (n >= 30 && n <= 37) set.c = n;
    else if (n >= 90 && n <= 97) set.c = n;
    else if (n >= 40 && n <= 47) set["bg" + n] = 1;
    else if (n === 39 || n === 49) { }
    else { }
  }
  var out = [];
  if (set.b) out.push("b");
  if (set.d) out.push("d");
  if (set.r) out.push("r");
  if (set.c) out.push("c" + set.c);
  for (var k in set) { if (k.indexOf("bg") === 0) out.push("b" + k); }
  this.style = out.join(" ");
};
NeohomeTerm.prototype.csi = function (seq) {
  // seq is everything between ESC[ and the final byte
  var final = seq.charAt(seq.length - 1);
  var params = seq.slice(0, -1);
  switch (final) {
    case "m": this.applySGR(params); break;
    case "J":
      if (params === "2" || params === "") this.clear();
      else this.eraseInLine();
      break;
    case "K": this.eraseInLine(); break;
    case "H": case "f": this.col = 0; break;
    case "C": this.col += Math.max(1, parseInt(params || "1", 10) || 1); break;
    case "D": this.col = Math.max(0, this.col - Math.max(1, parseInt(params || "1", 10) || 1)); break;
    default: break;
  }
};
NeohomeTerm.prototype.write = function (text) {
  var s = this.pending + text;
  this.pending = "";
  var i = 0;
  while (i < s.length) {
    var ch = s.charAt(i);
    if (ch === "\u001b") {
      var esc = s.slice(i);
      if (esc.length === 1) { this.pending = esc; return; }
      if (esc.charAt(1) === "[") {
        var m = esc.slice(2).match(/^[0-9;?]*[A-Za-z]/);
        if (!m) { this.pending = esc; return; }
        this.csi(m[0]);
        i += 2 + m[0].length;
        continue;
      }
      i += 2; // a two-byte escape this world does not use: drop it
      continue;
    }
    if (ch === "\r") { this.carriageReturn(); i++; continue; }
    if (ch === "\n") { this.newline(); i++; continue; }
    if (ch === "\b") { this.backspace(); i++; continue; }
    if (ch === "\t") {
      var stop = 8 - (this.col % 8);
      this.echo(new Array(stop + 1).join(" "));
      i++;
      continue;
    }
    if (ch === "\u0007") { i++; continue; } // bell: nothing to ring
    var cp = s.codePointAt(i);
    var chr = String.fromCodePoint(cp);
    this.echo(chr);
    i += chr.length;
  }
};
NeohomeTerm.prototype.rowsOut = function (limit) {
  var all = this.done.concat([this.line]);
  if (limit && all.length > limit) all = all.slice(all.length - limit);
  return all;
};
NeohomeTerm.prototype.snapshot = function (limit, keepStyles) {
  var all = this.rowsOut(limit);
  var out = [];
  for (var i = 0; i < all.length; i++) {
    var row = all[i];
    if (keepStyles) { out.push(row.slice()); continue; }
    var text = "";
    for (var j = 0; j < row.length; j++) text += row[j].t;
    out.push(text);
  }
  return out;
};
NeohomeTerm.prototype.cursorAtEnd = function () {
  return this.done.length + 1;
};
if (typeof module !== "undefined" && module.exports) { module.exports = NeohomeTerm; }
`

// pageJS is the browser half: it owns the socket, the keyboard and the DOM.
const pageJS = `
(function () {
  "use strict";
  var term = new NeohomeTerm(80, 24);
  var rowsEl = document.getElementById("rows");
  var stateEl = document.getElementById("state");
  var whoEl = document.getElementById("who");
  var loginEl = document.getElementById("login");
  var errEl = document.getElementById("err");
  var inputEl = document.getElementById("hidden");
  var ws = null;
  var history = [], histIdx = 0, editLine = "";
  var pendingOut = "";
  var raf = null;

  function setState(text, bad) {
    stateEl.textContent = text;
    stateEl.className = bad ? "state bad" : "state";
  }

  function esc(s) {
    return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
  }
  function render() {
    raf = null;
    var all = term.rowsOut(400);
    var html = [];
    for (var i = 0; i < all.length; i++) {
      var row = all[i];
      var line = "";
      for (var j = 0; j < row.length; j++) {
        var run = row[j];
        line += run.s ? '<span class="' + esc(run.s) + '">' + esc(run.t) + "</span>" : esc(run.t);
      }
      if (i === all.length - 1) {
        var col = term.col;
        var text = "";
        for (var k = 0; k < row.length; k++) text += row[k].t;
        if (text.length >= col) {
          line = esc(text.slice(0, col)) + '<span id="cursor"></span>' + esc(text.slice(col));
        } else {
          line += '<span id="cursor"></span>';
        }
      }
      html.push(line);
    }
    rowsEl.innerHTML = html.join("\n");
    window.scrollTo(0, document.body.scrollHeight);
  }
  function schedule() { if (!raf) raf = window.requestAnimationFrame(render); }

  function send(obj) {
    if (ws && ws.readyState === 1) { ws.send(JSON.stringify(obj)); return true; }
    return false;
  }
  function typeKey(ch) {
    // at a password prompt the client shows bullets for what it is sending:
    // the shell echoes nothing there, so this is the only feedback there is
    term.secretInput(ch);
    schedule();
    return send({ t: "in", d: ch });
  }

  function recall(dir) {
    if (history.length === 0) return;
    if (histIdx === history.length) editLine = currentInput();
    histIdx = Math.max(0, Math.min(history.length, histIdx + dir));
    var next = histIdx === history.length ? editLine : history[histIdx];
    var have = currentInput();
    var out = "";
    for (var i = 0; i < have.length; i++) out += "\u007f";
    typeKey(out + next);
  }
  // what the browser believes is on the current line: the terminal engine's
  // own text after the last prompt the server wrote
  function currentInput() {
    var text = term.lineText();
    var at = text.lastIndexOf("$ ");
    if (at >= 0) return text.slice(at + 2);
    var gt = text.lastIndexOf("> ");
    if (gt >= 0) return text.slice(gt + 2);
    return "";
  }

  function onFrame(data) {
    var msg;
    try { msg = JSON.parse(data); } catch (e) { return; }
    switch (msg.t) {
      case "out":
        term.write(msg.d || "");
        schedule();
        break;
      case "secret":
        term.setSecret(!!msg.on);
        break;
      case "exit":
        setState("closed", true);
        break;
      default: break;
    }
  }

  function connect(ticket) {
    var proto = location.protocol === "https:" ? "wss:" : "ws:";
    ws = new WebSocket(proto + "//" + location.host + "/ws?t=" + encodeURIComponent(ticket), "neohome.term.v1");
    ws.onopen = function () {
      setState("connected");
      loginEl.style.display = "none";
      inputEl.focus();
      send({ t: "resize", cols: cols(), rows: rows() });
    };
    ws.onmessage = function (ev) { onFrame(ev.data); };
    ws.onclose = function () {
      setState("closed", true);
      loginEl.style.display = "";
      errEl.textContent = "session ended";
    };
    ws.onerror = function () { setState("error", true); };
  }

  function cols() { return Math.max(40, Math.floor((document.getElementById("screen").clientWidth - 24) / 8.4)); }
  function rows() { return Math.max(10, Math.floor(window.innerHeight / 20) - 4); }

  // ---- keyboard: every key is forwarded as the bytes a terminal sends ----
  var KEYS = {
    Enter: "\r", Backspace: "\u007f", Tab: "\t", Escape: "\u001b",
    ArrowUp: null, ArrowDown: null, ArrowLeft: null, ArrowRight: null
  };
  document.addEventListener("keydown", function (ev) {
    if (loginEl.style.display !== "none") return;
    if (ev.metaKey) return;
    if (ev.ctrlKey && !ev.altKey) {
      var k = ev.key.toLowerCase();
      if (k === "c") { typeKey("\u0003"); ev.preventDefault(); return; }
      if (k === "d") { typeKey("\u0004"); ev.preventDefault(); return; }
      if (k === "l") { typeKey("\u000c"); ev.preventDefault(); return; }
      if (k === "z") { typeKey("\u001a"); ev.preventDefault(); return; }
      if (k === "u") { var have = currentInput(); var bs = ""; for (var i = 0; i < have.length; i++) bs += "\u007f"; typeKey(bs); ev.preventDefault(); return; }
      if (ev.key.length === 1) {
        typeKey(String.fromCharCode(ev.key.charCodeAt(0) & 31));
        ev.preventDefault();
      }
      return;
    }
    if (ev.altKey) { typeKey("\u001b" + ev.key); ev.preventDefault(); return; }
    if (ev.key === "ArrowUp") { recall(-1); ev.preventDefault(); return; }
    if (ev.key === "ArrowDown") { recall(1); ev.preventDefault(); return; }
    if (ev.key === "PageUp" || ev.key === "PageDown") { return; }
    if (KEYS.hasOwnProperty(ev.key) && KEYS[ev.key]) { typeKey(KEYS[ev.key]); ev.preventDefault(); return; }
    if (ev.key && ev.key.length === 1) { typeKey(ev.key); ev.preventDefault(); }
  });
  document.addEventListener("paste", function (ev) {
    if (loginEl.style.display !== "none") return;
    var text = (ev.clipboardData || window.clipboardData).getData("text") || "";
    typeKey(text.replace(/\r?\n/g, "\r"));
    ev.preventDefault();
  });
  window.addEventListener("resize", function () { send({ t: "resize", cols: cols(), rows: rows() }); });
  document.getElementById("screen").addEventListener("click", function () { inputEl.focus(); });
  document.getElementById("disc").addEventListener("click", function () {
    if (ws) ws.close(1000, "bye");
  });

  // remember submitted lines for arrow-up, exactly like a shell's history
  document.addEventListener("keydown", function (ev) {
    if (ev.key === "Enter" && loginEl.style.display === "none") {
      var line = currentInput().trim();
      if (line) { history.push(line); }
      histIdx = history.length;
    }
  }, true);

  // ---- login ----
  document.getElementById("loginform").addEventListener("submit", function (ev) {
    ev.preventDefault();
    errEl.textContent = "";
    var user = document.getElementById("u").value.trim();
    var pass = document.getElementById("p").value;
    fetch("/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ user: user, pass: pass })
    }).then(function (r) {
      return r.json().then(function (body) { return { ok: r.ok, status: r.status, body: body }; });
    }).then(function (res) {
      if (!res.ok) { errEl.textContent = (res.body && res.body.error) || ("login failed (" + res.status + ")"); return; }
      whoEl.textContent = res.body.user + "@" + res.body.device;
      document.getElementById("p").value = "";
      connect(res.body.ticket);
    }).catch(function (e) { errEl.textContent = "cannot reach the world: " + e; });
  });

  inputEl.focus();
})();
`
