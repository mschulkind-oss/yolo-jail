'use strict';
// The page of the read-only approval queue. Every string that came from yolo or an agent is put
// on the page as a text node; nothing request-derived becomes markup, a link, an attribute, a
// class or a style. Class names below are fixed literals or picked from a fixed table.
(function () {
  var TOKEN_KEY = 'yolo-queue-token';
  var LINK_DEAD = "This link was used or expired: press Enter in the queue's terminal for a new one.";
  var LINK_RACE = "This link was already used by something else on this machine. Every session was ended and the terminal has a new link: treat this as a possible intruder.";
  var BANNER_CLASS = { 'live': 'live', 'stale': 'stale', 'never-loaded': 'never' };
  var REVEAL = {}; // "request id#index" -> true while one masked value is shown
  var current = null;
  var lastKey = '';
  var remainingSpans = {};
  var lastMessageAt = 0;
  var connected = false;
  var aborter = null;
  var stopped = false;

  function el(tag, cls, text) {
    var n = document.createElement(tag);
    if (cls) { n.className = cls; }
    if (text !== undefined && text !== null) { n.textContent = String(text); }
    return n;
  }

  function banner(cls, text) {
    var b = document.getElementById('banner');
    b.className = cls;
    b.textContent = text;
  }

  function main() { return document.getElementById('main'); }

  function showLinkDead() {
    stopped = true;
    banner('never', LINK_DEAD);
    main().replaceChildren();
  }

  function row(dl, label, value) {
    if (value === undefined || value === null || value === '') { return null; }
    dl.appendChild(el('dt', '', label));
    var dd = el('dd', '', value);
    dl.appendChild(dd);
    return dd;
  }

  function lines(dl, label, list) {
    if (!list || list.length === 0) { return; }
    row(dl, label, list.join('\n'));
  }

  function maskText(it) {
    return '[hidden: ' + it.length + ' characters, sha256 ' + it.sha + ']';
  }

  function argvList(reqId, items) {
    var ol = el('ol', 'argv');
    items.forEach(function (it, i) {
      var li = el('li');
      if (!it.secret) {
        li.textContent = it.value;
        ol.appendChild(li);
        return;
      }
      var key = reqId + '#' + i;
      li.appendChild(el('span', '', it.prefix));
      var val = el('span', 'mask');
      var btn = el('button', '');
      btn.type = 'button';
      function paint() {
        var shown = REVEAL[key] === true;
        val.textContent = shown ? it.value : maskText(it);
        btn.textContent = shown ? 'Hide' : 'Show';
      }
      btn.addEventListener('click', function () {
        if (REVEAL[key]) { delete REVEAL[key]; } else { REVEAL[key] = true; }
        paint();
      });
      paint();
      li.appendChild(val);
      li.appendChild(btn);
      ol.appendChild(li);
    });
    return ol;
  }

  function remainingText(s) {
    if (s <= 0) { return 'past its expiry, not yet removed by yolo'; }
    var m = Math.floor(s / 60);
    return m > 0 ? m + ' min ' + (s % 60) + ' s left' : s + ' s left';
  }

  function pendingCard(p) {
    var card = el('section', 'card');
    card.appendChild(el('h3', '', p.headline));
    var dl = el('dl');
    row(dl, 'Request', p.id);
    row(dl, 'State', p.state);
    row(dl, 'Source', p.source + (p.source_status ? ' (' + p.source_status + ')' : ''));
    row(dl, 'Authority', p.authority);
    row(dl, 'Jail', p.jail);
    row(dl, 'Workspace', p.workspace);
    row(dl, 'Reach', 'A grant from this request is held by the workspace above: every jail of that workspace may use it.');
    row(dl, 'Set', p.set);
    row(dl, 'Repository', p.repo);
    row(dl, 'Action', p.action);
    row(dl, 'Target', p.target);
    row(dl, 'Filed', p.filed);
    row(dl, 'Expires', p.expires);
    var rem = row(dl, 'Time left', remainingText(p.remaining_s));
    if (rem) { remainingSpans[p.id] = rem; }
    lines(dl, 'Needs approval for', p.needs_approval);
    row(dl, 'Hook failure', p.hook_failure);
    row(dl, 'Command digest', p.command_digest);
    row(dl, 'Stdin digest', p.stdin_digest);
    row(dl, 'Command (masked)', p.command);
    card.appendChild(dl);
    card.appendChild(el('h3', '', 'Arguments, one per line'));
    card.appendChild(argvList(p.id, p.argv));
    var agent = el('div', 'agent');
    agent.appendChild(el('div', 'label', 'Agent-supplied text (written or influenced by the agent; do not trust it)'));
    var adl = el('dl');
    row(adl, 'Title', p.title);
    lines(adl, 'Refs', p.refs);
    row(adl, 'Description', p.description);
    row(adl, 'Hook output', p.hook_output);
    agent.appendChild(adl);
    card.appendChild(agent);
    return card;
  }

  function table(headers, rows) {
    var t = el('table');
    var tr = el('tr');
    headers.forEach(function (h) { tr.appendChild(el('th', '', h)); });
    t.appendChild(tr);
    rows.forEach(function (r) {
      var x = el('tr');
      r.forEach(function (c) { x.appendChild(el('td', '', c)); });
      t.appendChild(x);
    });
    return t;
  }

  function render(m) {
    current = m;
    lastMessageAt = Date.now();
    var f = m.freshness;
    if (!connected) { connected = true; }
    banner(BANNER_CLASS[f.state] || 'stale', f.message);
    main().classList.remove('grey');
    var key = JSON.stringify(m, function (k, v) {
      return (k === 'now' || k === 'checked_ago_s' || k === 'remaining_s' || k === 'message') ? undefined : v;
    });
    if (key === lastKey) {
      m.pending.forEach(function (p) {
        var s = remainingSpans[p.id];
        if (s) { s.textContent = remainingText(p.remaining_s); }
      });
      return;
    }
    lastKey = key;
    paint(m);
  }

  function paint(m) {
    var root = main();
    root.replaceChildren();
    remainingSpans = {};
    var f = m.freshness;
    if (f.state === 'never-loaded') {
      root.appendChild(el('p', 'note', 'Nothing has loaded yet, so there is no list to show. This is not "nothing pending".'));
      return;
    }
    root.appendChild(el('h2', '', 'Pending requests (' + m.pending.length + ')'));
    if (m.pending.length === 0) {
      root.appendChild(el('p', 'note', f.state === 'live'
        ? 'No pending requests.'
        : 'The list is empty, but the state is stale: do not read this as "nothing pending".'));
    }
    m.pending.forEach(function (p) { root.appendChild(pendingCard(p)); });

    root.appendChild(el('h2', '', 'Live grants (' + m.grants.length + ')'));
    root.appendChild(el('p', 'note', 'A grant belongs to its workspace: every jail of that workspace may use it.'));
    if (m.grants.length > 0) {
      root.appendChild(table(['Grant', 'Source', 'Workspace', 'Jail', 'Set', 'Coverage', 'Uses left', 'Expires', 'Request', 'Used by'],
        m.grants.map(function (g) {
          return [g.id, g.source, g.workspace, g.jail, g.set, g.coverage, String(g.uses_left), g.expires, g.request, g.used_by];
        })));
    }
    root.appendChild(el('h2', '', 'Working set (' + m.working.length + ')'));
    if (m.working.length > 0) {
      root.appendChild(table(['Entry', 'Source', 'Workspace', 'Repository', 'Kind', 'Resource', 'By', 'Added', 'Expires', 'File'],
        m.working.map(function (w) {
          return [w.id, w.source, w.workspace, w.repo, w.kind, w.resource, w.by, w.added, w.expires, w.file];
        })));
    }
    root.appendChild(el('h2', '', 'Sources'));
    if (m.sources.length === 0) {
      root.appendChild(el('p', 'note', 'yolo listed no sources.'));
    } else {
      root.appendChild(table(['Source', 'Status', 'Authority', 'Selection basis'],
        m.sources.map(function (s) { return [s.source, s.status, s.authority, s.selection_basis]; })));
    }
    if (m.diagnostics.length > 0) {
      root.appendChild(el('h2', '', 'Diagnostics'));
      var ul = el('ul');
      m.diagnostics.forEach(function (d) { ul.appendChild(el('li', '', d)); });
      root.appendChild(ul);
    }
    root.appendChild(el('h2', '', 'History since this queue started'));
    root.appendChild(el('p', 'note', m.history.note));
    if (m.history.gaps.length > 0) {
      var gl = el('ul');
      m.history.gaps.forEach(function (g) { gl.appendChild(el('li', '', g)); });
      root.appendChild(gl);
    }
    if (m.history.entries.length === 0) {
      root.appendChild(el('p', 'note', 'Nothing observed yet.'));
    } else {
      root.appendChild(table(['Time', 'Kind', 'Source', 'Workspace', 'Jail', 'What', 'Outcome', 'Id'],
        m.history.entries.map(function (e) { return [e.time, e.kind, e.source, e.workspace, e.jail, e.label, e.outcome, e.key]; })));
    }
    if (m.malformed_lines > 0) {
      root.appendChild(el('p', 'note', 'Unreadable watch lines discarded since start: ' + m.malformed_lines));
    }
  }

  function disconnected() {
    connected = false;
    banner('disconnected', 'Disconnected from the queue; reconnecting');
    main().classList.add('grey');
  }

  function sleep(ms) { return new Promise(function (r) { setTimeout(r, ms); }); }

  async function follow(token) {
    var delay = 1000;
    while (!stopped) {
      aborter = new AbortController();
      try {
        var res = await fetch('/api/stream', { headers: { 'Authorization': 'Bearer ' + token }, signal: aborter.signal, cache: 'no-store' });
        if (res.status === 401) {
          sessionStorage.removeItem(TOKEN_KEY);
          showLinkDead();
          return;
        }
        if (!res.ok || !res.body) { throw new Error('stream status ' + res.status); }
        var reader = res.body.getReader();
        var dec = new TextDecoder();
        var buf = '';
        for (;;) {
          var chunk = await reader.read();
          if (chunk.done) { break; }
          buf += dec.decode(chunk.value, { stream: true });
          var at;
          while ((at = buf.indexOf('\n\n')) >= 0) {
            var block = buf.slice(0, at);
            buf = buf.slice(at + 2);
            block.split('\n').forEach(function (l) {
              if (l.indexOf('data: ') === 0) {
                render(JSON.parse(l.slice(6)));
                delay = 1000;
              }
            });
          }
        }
      } catch (e) {
        // fall through to the reconnect below
      }
      if (stopped) { return; }
      disconnected();
      await sleep(delay);
      delay = Math.min(delay * 2, 15000);
    }
  }

  async function start() {
    var hash = location.hash;
    var code = '';
    if (hash.indexOf('#code=') === 0) {
      // Clear the address bar first, whatever the fragment holds.
      var raw = hash.slice(6);
      history.replaceState(null, '', '/');
      try {
        code = decodeURIComponent(raw);
      } catch (e) {
        showLinkDead();
        return;
      }
    }
    if (code) {
      try {
        var res = await fetch('/api/session', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ code: code }),
          cache: 'no-store'
        });
        if (res.status === 200) {
          var body = await res.json();
          sessionStorage.setItem(TOKEN_KEY, body.token);
        } else if (res.status === 409) {
          // The server revoked every session; our own stored token is dead too.
          sessionStorage.removeItem(TOKEN_KEY);
          stopped = true;
          banner('never', LINK_RACE);
          main().replaceChildren();
          return;
        } else {
          showLinkDead();
          return;
        }
      } catch (e) {
        banner('never', 'Could not reach the queue to exchange the link; is it still running?');
        return;
      }
    }
    var token = sessionStorage.getItem(TOKEN_KEY);
    if (!token) { showLinkDead(); return; }
    setInterval(function () {
      if (!stopped && connected && Date.now() - lastMessageAt > 15000) {
        if (aborter) { aborter.abort(); }
      }
    }, 1000);
    follow(token);
  }

  document.addEventListener('visibilitychange', function () {
    if (document.hidden) {
      REVEAL = {};
      if (current) { lastKey = ''; paint(current); }
    }
  });

  start();
})();
