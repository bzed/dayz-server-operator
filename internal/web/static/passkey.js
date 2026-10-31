// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Passkeys on the login pages: asks the browser for an assertion (second step) or a new credential (enrolment)
// and hands the answer to the server. The options come from the server as WebAuthn JSON.
(function () {
  'use strict';

  function b64uToBuf(s) {
    s = s.replace(/-/g, '+').replace(/_/g, '/');
    while (s.length % 4) s += '=';
    var bin = atob(s), buf = new Uint8Array(bin.length);
    for (var i = 0; i < bin.length; i++) buf[i] = bin.charCodeAt(i);
    return buf.buffer;
  }

  function bufToB64u(buf) {
    var bytes = new Uint8Array(buf), bin = '';
    for (var i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]);
    return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  }

  function descriptors(list) {
    return (list || []).map(function (d) {
      d.id = b64uToBuf(d.id);
      return d;
    });
  }

  var token = (document.querySelector('meta[name=csrf]') || {}).content || '';

  function post(url, body) {
    return fetch(url, {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': token },
      body: body === undefined ? '{}' : JSON.stringify(body)
    }).then(function (r) {
      return r.json().then(function (j) {
        if (!r.ok) throw new Error(j.error || 'request failed');
        return j;
      });
    });
  }

  function fail(err) {
    var box = document.getElementById('passkey-error');
    if (!box) return;
    box.textContent = err && err.name === 'NotAllowedError' ? 'The passkey was not used.' : (err && err.message) || 'The passkey did not work.';
    box.hidden = false;
  }

  function creationOptions(o) {
    var pk = o.publicKey;
    pk.challenge = b64uToBuf(pk.challenge);
    pk.user.id = b64uToBuf(pk.user.id);
    pk.excludeCredentials = descriptors(pk.excludeCredentials);
    return o;
  }

  function requestOptions(o) {
    var pk = o.publicKey;
    pk.challenge = b64uToBuf(pk.challenge);
    pk.allowCredentials = descriptors(pk.allowCredentials);
    return o;
  }

  function attestation(c) {
    var r = c.response;
    return {
      id: c.id,
      rawId: bufToB64u(c.rawId),
      type: c.type,
      authenticatorAttachment: c.authenticatorAttachment || undefined,
      clientExtensionResults: c.getClientExtensionResults ? c.getClientExtensionResults() : {},
      response: {
        clientDataJSON: bufToB64u(r.clientDataJSON),
        attestationObject: bufToB64u(r.attestationObject),
        transports: r.getTransports ? r.getTransports() : []
      }
    };
  }

  function assertion(c) {
    var r = c.response;
    return {
      id: c.id,
      rawId: bufToB64u(c.rawId),
      type: c.type,
      authenticatorAttachment: c.authenticatorAttachment || undefined,
      clientExtensionResults: c.getClientExtensionResults ? c.getClientExtensionResults() : {},
      response: {
        clientDataJSON: bufToB64u(r.clientDataJSON),
        authenticatorData: bufToB64u(r.authenticatorData),
        signature: bufToB64u(r.signature),
        userHandle: r.userHandle ? bufToB64u(r.userHandle) : undefined
      }
    };
  }

  function login() {
    return post('/login/passkey/begin')
      .then(function (opts) { return navigator.credentials.get(requestOptions(opts)); })
      .then(function (cred) { return post('/login/passkey/finish', { response: assertion(cred) }); })
      .then(function (res) { window.location.href = res.next || '/'; });
  }

  function enrol() {
    var nameInput = document.getElementById('passkey-name');
    return post('/login/enrol/passkey/begin')
      .then(function (opts) { return navigator.credentials.create(creationOptions(opts)); })
      .then(function (cred) {
        return post('/login/enrol/passkey/finish', { response: attestation(cred), name: nameInput ? nameInput.value : '' });
      })
      .then(function (res) {
        var codes = document.getElementById('recovery-codes');
        if (codes) {
          codes.textContent = (res.recovery_codes || []).join('\n');
          document.getElementById('recovery-continue').href = res.next || '/';
          document.getElementById('recovery-after').hidden = false;
        } else {
          window.location.href = res.next || '/';
        }
      });
  }

  function account() {
    var nameInput = document.getElementById('passkey-name');
    var pending;
    return post('/account/passkeys/begin')
      .then(function (res) { pending = res.pending; return navigator.credentials.create(creationOptions(res.options)); })
      .then(function (cred) {
        return post('/account/passkeys/finish', { pending: pending, response: attestation(cred), name: nameInput ? nameInput.value : '' });
      })
      .then(function (res) { window.location.href = res.next || '/account'; });
  }

  document.addEventListener('click', function (ev) {
    var btn = ev.target.closest ? ev.target.closest('[data-passkey]') : null;
    if (!btn) return;
    if (!window.PublicKeyCredential) {
      fail(new Error('This browser has no passkey support.'));
      return;
    }
    ev.preventDefault();
    var box = document.getElementById('passkey-error');
    if (box) box.hidden = true;
    var kind = btn.dataset.passkey;
    (kind === 'enrol' ? enrol() : kind === 'account' ? account() : login()).catch(fail);
  });
})();
