// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Action panels of the player and vehicle tables open as a row under the one they belong to. The
// tables reload by polling, which would throw an open panel away, so the polling waits while one is open.
(function () {
  'use strict';
  document.addEventListener('htmx:beforeRequest', function (e) {
    var el = e.detail.elt;
    if (el.hasAttribute('data-poll') && document.querySelector('.rowpanel')) {
      e.preventDefault();
    } else if (el.hasAttribute('data-rowpanel')) {
      document.querySelectorAll('.rowpanel').forEach(function (r) { r.remove(); });
    }
  });
  document.addEventListener('click', function (e) {
    var b = e.target.closest('[data-close]');
    if (b) {
      var row = b.closest('.rowpanel');
      if (row) row.remove();
    }
  });
})();
