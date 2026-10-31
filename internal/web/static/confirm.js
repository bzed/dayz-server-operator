// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Asks before a form with data-confirm is sent (the content security policy forbids onsubmit="").
document.addEventListener('submit', function (ev) {
  var msg = ev.target && ev.target.dataset ? ev.target.dataset.confirm : '';
  if (msg && !window.confirm(msg)) ev.preventDefault();
});
