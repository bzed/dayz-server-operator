// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Action panels of the player and vehicle tables open as a row under the one they belong to. The
// tables reload by polling, which would throw an open panel away, so the polling waits while one is open.
(function () {
  'use strict';
  document.addEventListener('htmx:beforeRequest', function (e) {
    var el = e.detail.elt;
    if (el.hasAttribute('data-poll') && document.querySelector('.rowpanel, td.who.open')) {
      e.preventDefault();
    } else if (el.hasAttribute('data-rowpanel')) {
      document.querySelectorAll('.rowpanel').forEach(function (r) { r.remove(); });
    }
  });
  // The connection info over a name shows on hover and focus, and on a tap of its i button (touch
  // screens have no hover). It is placed under the name with fixed positioning, so that the scrolling
  // table does not clip it.
  function placePop(cell) {
    var pop = cell.querySelector('.pop');
    if (!pop) return;
    var r = cell.getBoundingClientRect();
    pop.style.left = Math.max(8, Math.min(r.left, window.innerWidth - pop.offsetWidth - 8)) + 'px';
    pop.style.top = (r.bottom + 2) + 'px';
  }
  document.addEventListener('mouseover', function (e) {
    var c = e.target.closest && e.target.closest('td.who');
    if (c) placePop(c);
  });
  document.addEventListener('click', function (e) {
    var b = e.target.closest('[data-info]');
    var open = document.querySelector('td.who.open');
    if (open && (!b || b.closest('td.who') !== open)) {
      open.classList.remove('open');
      open.querySelector('[data-info]').setAttribute('aria-expanded', 'false');
    }
    if (!b) return;
    var cell = b.closest('td.who');
    var on = cell.classList.toggle('open');
    b.setAttribute('aria-expanded', on ? 'true' : 'false');
    if (on) {
      if (!cell.querySelector('.pop').childElementCount) htmx.trigger(cell, 'focusin');
      placePop(cell);
    }
  });
  document.addEventListener('keydown', function (e) {
    var open = e.key === 'Escape' && document.querySelector('td.who.open');
    if (open) open.classList.remove('open');
  });
  document.addEventListener('click', function (e) {
    var b = e.target.closest('[data-close]');
    if (b) {
      var row = b.closest('.rowpanel');
      if (row) row.remove();
    }
  });

  // A preset button (data-set="max" data-for="quantity") fills the form field of that name.
  document.addEventListener('click', function (e) {
    var b = e.target.closest('[data-set]');
    var f = b && b.closest('form');
    var inp = f && f.elements[b.getAttribute('data-for')];
    if (inp) inp.value = b.getAttribute('data-set');
  });

  // Clicking a header sorts the table by that column. The tables are replaced on every poll, so the
  // chosen column and direction are kept per table (keyed by the polling element) and applied again
  // after each swap.
  var sorts = {};
  function key(table) {
    var host = table.closest('[data-poll]');
    return host ? host.getAttribute('hx-get') : '';
  }
  // Until a header is clicked a table is sorted by the columns in its data-sort-default (column
  // indexes, most significant first); without one, by the first column, the name. A clicked column
  // goes first and the defaults break its ties.
  function defaults(table) {
    var d = table.getAttribute('data-sort-default');
    return d ? d.split(',').map(Number) : [0];
  }
  function current(table) {
    return sorts[key(table)] || {cols: defaults(table), dir: 1};
  }
  function cell(row, i) {
    return row.cells[i] ? row.cells[i].textContent.trim() : '';
  }
  function compare(a, b, col, num) {
    var x = cell(a, col), y = cell(b, col);
    if (!num) return x.localeCompare(y, undefined, {sensitivity: 'base', numeric: true});
    var nx = parseFloat(x), ny = parseFloat(y);
    if (isNaN(nx)) nx = -Infinity;
    if (isNaN(ny)) ny = -Infinity;
    return nx === ny ? 0 : (nx < ny ? -1 : 1);
  }
  function applySort(table) {
    var st = current(table);
    var ths = table.querySelectorAll('thead th');
    ths.forEach(function (th, i) {
      th.setAttribute('aria-sort', st.cols[0] === i ? (st.dir > 0 ? 'ascending' : 'descending') : 'none');
    });
    var body = table.tBodies[0];
    var rows = Array.prototype.slice.call(body.rows).filter(function (r) { return !r.classList.contains('rowpanel'); });
    rows.sort(function (a, b) {
      for (var i = 0; i < st.cols.length; i++) {
        var col = st.cols[i];
        var r = compare(a, b, col, ths[col].getAttribute('data-sort') === 'num');
        if (r !== 0) return r * (i === 0 ? st.dir : 1);
      }
      return 0;
    });
    rows.forEach(function (r) { body.appendChild(r); });
  }
  document.addEventListener('click', function (e) {
    var th = e.target.closest('th[data-sort]');
    if (!th) return;
    var table = th.closest('table');
    var col = th.cellIndex, cur = current(table);
    sorts[key(table)] = {
      cols: [col].concat(defaults(table).filter(function (c) { return c !== col; })),
      dir: cur.cols[0] === col ? -cur.dir : 1
    };
    document.querySelectorAll('.rowpanel').forEach(function (r) { r.remove(); });
    applySort(table);
  });
  document.addEventListener('htmx:afterSettle', function (e) {
    var t = e.target.querySelector && e.target.querySelector('table');
    if (t) applySort(t);
  });
})();
