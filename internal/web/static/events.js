// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// The events page: a "tail -f" of the instance's event feed. The rows are rendered by the server
// (html/template escapes every name and chat line); this script only appends them. A feed event on the
// stream is a signal to fetch the rows after the last one shown, so a reconnect fills the gap the same way.
(function () {
  'use strict';
  var log = document.getElementById('evlog');
  if (!log) { return; }
  var MAX_ROWS = 1000;
  var fresh = document.getElementById('evnew');
  var conn = document.getElementById('evconn');
  var last = parseInt(log.getAttribute('data-last'), 10) || 0;
  var unseen = 0, fetching = false, again = false;

  function atBottom() { return log.scrollHeight - log.scrollTop - log.clientHeight < 24; }
  function toBottom() { log.scrollTop = log.scrollHeight; unseen = 0; fresh.hidden = true; }
  function localTime(li) {
    var t = li.querySelector('time');
    var d = t && new Date(t.getAttribute('datetime'));
    if (d && !isNaN(d)) { t.textContent = d.toLocaleTimeString([], {hour12: false}); }
  }

  function append(html) {
    var tpl = document.createElement('template');
    tpl.innerHTML = html;
    var follow = atBottom();
    var added = 0;
    tpl.content.querySelectorAll('li.ev').forEach(function (li) {
      var id = parseInt(li.getAttribute('data-id'), 10) || 0;
      if (id <= last) { return; }
      last = id;
      localTime(li);
      log.appendChild(li);
      added++;
    });
    while (log.children.length > MAX_ROWS) { log.removeChild(log.firstChild); }
    if (!added) { return; }
    if (follow) { toBottom(); } else { unseen += added; fresh.hidden = false; }
  }

  // One fetch at a time; events that arrive meanwhile are fetched right after.
  function fetchRows() {
    if (fetching) { again = true; return; }
    fetching = true;
    fetch(log.getAttribute('data-feed') + '?after=' + last, {credentials: 'same-origin'})
      .then(function (r) { return r.ok ? r.text() : ''; })
      .then(append)
      .catch(function () {})
      .then(function () {
        fetching = false;
        if (again) { again = false; fetchRows(); }
      });
  }

  var src = new EventSource(log.getAttribute('data-stream'));
  src.addEventListener('feed', fetchRows);
  // Every (re)connect fetches what came in meanwhile: before the first connect, or while the stream was down.
  src.onopen = function () { conn.classList.remove('on'); fetchRows(); };
  src.onerror = function () { conn.classList.add('on'); };

  fresh.addEventListener('click', toBottom);
  log.addEventListener('scroll', function () { if (atBottom()) { unseen = 0; fresh.hidden = true; } });

  // The filter checkboxes hide rows of a group with a class on the log; the choice is remembered.
  var boxes = document.querySelectorAll('[data-filters] input[data-group]');
  var hidden = [];
  try { hidden = JSON.parse(localStorage.getItem('dzo.events.hidden') || '[]'); } catch (e) { hidden = []; }
  function apply() {
    var off = [];
    boxes.forEach(function (b) {
      var g = b.getAttribute('data-group');
      log.classList.toggle('hide-' + g, !b.checked);
      if (!b.checked) { off.push(g); }
    });
    try { localStorage.setItem('dzo.events.hidden', JSON.stringify(off)); } catch (e) { /* private window */ }
  }
  boxes.forEach(function (b) {
    b.checked = hidden.indexOf(b.getAttribute('data-group')) < 0;
    b.addEventListener('change', function () { apply(); if (atBottom()) { toBottom(); } });
  });
  apply();

  log.querySelectorAll('li.ev').forEach(localTime);
  toBottom();
  // The chat line keeps the focus after Enter sent a message (rows.js resets the form).
  var input = document.querySelector('.evinput input[name=text]');
  document.addEventListener('htmx:afterRequest', function (e) { if (input && e.detail.elt.matches('.evinput')) { input.focus(); } });
})();
