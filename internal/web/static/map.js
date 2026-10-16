// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Live admin map. World coordinates are metres: x east, z north, so Leaflet's
// simple CRS maps lng = x and lat = z. The background is the map's satellite
// imagery as an XYZ tile pyramid if the instance's map has one (its metadata is
// in data-meta); the transformation below makes zoom levels line up with the
// pyramid while coordinates stay in metres. Without tiles there is a plain
// grid. All text from the server goes in through textContent.
//
// Icons sit exactly on their position (the marker root is 0x0, see app.css), so
// dragging a player icon and dropping it teleports the player to the centre of
// the icon, shown as crosshair and in the coordinate readout while dragging.
(function () {
  'use strict';
  var el = document.getElementById('map');
  if (!el || typeof L === 'undefined') { return; }
  var size = parseInt(el.dataset.size, 10) || 15360;
  var base = el.dataset.base;
  var csrf = document.querySelector('meta[name=csrf]').content;
  var rank = { viewer: 0, moderator: 1, operator: 2, admin: 3 };
  var myRank = rank[el.dataset.role] || 0;
  function can(min) { return myRank >= rank[min || 'viewer']; }
  var still = window.matchMedia('(prefers-reduced-motion: reduce)').matches;

  // ---- map and fit ----

  // A divIcon that holds a DOM element. Leaflet 1.9's divIcon takes one as
  // html, Debian's 1.7 only takes a string, so build it the way both agree on.
  var ElIcon = L.DivIcon.extend({
    createIcon: function (old) {
      var d = L.DivIcon.prototype.createIcon.call(this, old);
      d.textContent = '';
      d.appendChild(this.options.el);
      return d;
    }
  });
  function elIcon(className, el) { return new ElIcon({ className: className, el: el, html: '', iconSize: [0, 0], iconAnchor: [0, 0] }); }

  var meta = null;
  try { meta = el.dataset.meta ? JSON.parse(el.dataset.meta) : null; } catch (e) { meta = null; }
  // The imagery covers [x0, x0+W] x [z0, z0+H] metres. One pixel at zoom z is
  // 1/(a*2^z) metres, so a = 1 is one pixel per metre at zoom 0.
  var x0 = 0, z0 = 0, W = size, H = size, a = 1, crs = L.CRS.Simple;
  if (meta) {
    x0 = meta.x0; z0 = meta.z0; W = meta.width; H = meta.height;
    a = meta.px_per_m / Math.pow(2, meta.max_zoom);
    crs = L.extend({}, L.CRS.Simple, { transformation: new L.Transformation(a, -a * x0, -a, a * (z0 + H)) });
    el.classList.add('tiles');
    if (meta.pad) { el.style.background = meta.pad; } // outside the imagery, same as the edge tiles
  }
  var bounds = L.latLngBounds([[z0, x0], [z0 + H, x0 + W]]);
  var map = L.map(el, {
    crs: crs, zoomSnap: 0, zoomDelta: 0.5, wheelPxPerZoomLevel: 90,
    maxZoom: Math.log2(8 / a), // 8 pixels per metre
    maxBoundsViscosity: 0.8, zoomControl: false, attributionControl: false,
    bounceAtZoomLimits: false
  });
  var minorZ = Math.log2(0.2 / a); // the 100 m grid shows from 5 m per pixel
  if (meta) {
    L.tileLayer(el.dataset.tiles + '/' + meta.hash + '/{z}/{x}/{y}.' + meta.ext, {
      tileSize: meta.tile_size, minZoom: -12, minNativeZoom: 0, maxNativeZoom: meta.max_zoom,
      bounds: bounds, noWrap: true, keepBuffer: 4, updateWhenZooming: false
    }).addTo(map);
  }
  L.control.zoom({ position: 'bottomright' }).addTo(map);
  var fitZ = null;
  var fitBtn = L.control({ position: 'bottomright' });
  fitBtn.onAdd = function () {
    var d = L.DomUtil.create('div', 'leaflet-bar');
    var a = L.DomUtil.create('a', 'fit', d);
    a.href = '#';
    a.title = 'Show the whole map';
    a.setAttribute('role', 'button');
    a.textContent = 'Fit';
    L.DomEvent.disableClickPropagation(d);
    L.DomEvent.on(a, 'click', function (e) { L.DomEvent.preventDefault(e); toFit(true); });
    return d;
  };
  fitBtn.addTo(map);
  L.control.scale({ imperial: false, position: 'bottomleft' }).addTo(map);

  // The smallest zoom is the one that shows the whole world in this window, so
  // the map always fits, and a window that is resized while showing the whole
  // world keeps showing it.
  function toFit(animate) {
    map.setView(bounds.getCenter(), fitZ, { animate: !!animate && !still });
  }
  function refit() {
    if (!el.clientWidth || !el.clientHeight) { return; }
    map.invalidateSize({ pan: false });
    map.setMinZoom(-12); // getBoundsZoom clamps to the current minimum
    var z = map.getBoundsZoom(bounds, false, L.point(10, 10));
    var wasFit = fitZ === null || Math.abs(map.getZoom() - fitZ) < 0.02;
    fitZ = z;
    map.setMinZoom(z);
    map.setMaxBounds(bounds.pad(0.15));
    if (wasFit) { toFit(false); }
    el.classList.toggle('far', map.getZoom() < fitZ + 0.9);
  }
  refit();
  var pending = 0;
  new ResizeObserver(function () {
    cancelAnimationFrame(pending);
    pending = requestAnimationFrame(refit);
  }).observe(el);
  map.on('zoomend', function () {
    el.classList.toggle('far', map.getZoom() < fitZ + 0.9);
    minor.toggle(map.getZoom() >= minorZ);
  });

  // ---- grid ----

  function gridLines(step) {
    var out = [];
    for (var gx = Math.ceil(x0 / step) * step; gx < x0 + W; gx += step) { if (gx > x0) { out.push([[z0, gx], [z0 + H, gx]]); } }
    for (var gz = Math.ceil(z0 / step) * step; gz < z0 + H; gz += step) { if (gz > z0) { out.push([[gz, x0], [gz, x0 + W]]); } }
    return out;
  }
  L.rectangle(bounds, { className: 'world', interactive: false }).addTo(map);
  var minorLayer = L.polyline(gridLines(100), { className: 'g-minor', interactive: false, smoothFactor: 0 });
  var minor = { toggle: function (on) { if (on && !map.hasLayer(minorLayer)) { minorLayer.addTo(map); } else if (!on && map.hasLayer(minorLayer)) { map.removeLayer(minorLayer); } } };
  L.polyline(gridLines(1000), { className: 'g-major', interactive: false, smoothFactor: 0 }).addTo(map);
  for (var k = Math.ceil(Math.max(x0, z0) / 1000) * 1000; k < Math.min(x0 + W, z0 + H); k += 1000) {
    [['', [z0, k]], ['v', [k, x0]]].forEach(function (d) {
      var box = document.createElement('div');
      var s = document.createElement('span');
      s.textContent = String(k / 1000);
      box.appendChild(s);
      L.marker(d[1], { interactive: false, keyboard: false, icon: elIcon('gl ' + d[0], box) }).addTo(map);
    });
  }
  minor.toggle(map.getZoom() >= minorZ);

  var players = L.layerGroup().addTo(map);
  var vehicles = L.layerGroup().addTo(map);
  var events = L.layerGroup().addTo(map);
  var control = L.control.layers(null, { Players: players, Vehicles: vehicles, Events: events }, { collapsed: true, position: 'topright' }).addTo(map);
  var markerLayers = {};

  // Coordinates under the pointer, in metres.
  var hud = L.control({ position: 'bottomleft' });
  var hudText = null;
  hud.onAdd = function () {
    var d = L.DomUtil.create('div', 'hud');
    hudText = document.createTextNode('x –   z –');
    d.appendChild(hudText);
    return d;
  };
  hud.addTo(map);
  function showPos(label, ll) { hudText.nodeValue = label + ' x ' + Math.round(ll.lng * 10) / 10 + '   z ' + Math.round(ll.lat * 10) / 10; }
  var hudFrame = 0;
  map.on('mousemove', function (e) {
    cancelAnimationFrame(hudFrame);
    hudFrame = requestAnimationFrame(function () { showPos('', e.latlng); });
  });

  // ---- helpers ----

  function ll(o) { return [o.z, o.x]; }
  function r1(n) { return Math.round(n * 10) / 10; }
  function mk(tag, cls, txt) {
    var n = document.createElement(tag);
    if (cls) { n.className = cls; }
    if (txt != null) { n.textContent = txt; }
    return n;
  }
  function posText(o) { return Math.round(o.x) + ' / ' + Math.round(o.z); }

  function post(path, params) {
    return fetch(base + path, {
      method: 'POST',
      headers: { 'X-CSRF-Token': csrf, 'Content-Type': 'application/x-www-form-urlencoded' },
      body: new URLSearchParams(params)
    }).then(function (r) { return r.text(); });
  }

  var toastTimer = 0;
  function toast(node) {
    var f = document.getElementById('flash');
    if (!f) { return; }
    f.textContent = '';
    f.appendChild(node);
    clearTimeout(toastTimer);
    toastTimer = setTimeout(function () { f.textContent = ''; }, 6000);
  }
  function toastText(ok, text) {
    var d = mk('div', 'flash ' + (ok ? 'ok' : 'err'), text);
    d.setAttribute('role', 'status');
    toast(d);
  }
  // do posts an action and reports it; the answer is the server's flash fragment.
  function act(path, params) {
    return post(path, params).then(function (html) {
      var doc = new DOMParser().parseFromString(html, 'text/html');
      var f = doc.querySelector('.flash');
      var ok = !!f && f.classList.contains('ok');
      toastText(ok, f ? f.textContent : 'unexpected answer');
      return ok;
    }, function (err) {
      toastText(false, 'request failed: ' + err.message);
      return false;
    });
  }
  function copy(text) {
    var p = navigator.clipboard ? navigator.clipboard.writeText(text) : Promise.reject(new Error('clipboard unavailable'));
    p.then(function () { toastText(true, 'Copied ' + text); }, function () { toastText(false, 'Could not copy; select it by hand: ' + text); });
  }
  function zoomTo(latlng, radius) {
    if (radius) {
      var c = L.latLng(latlng), r = radius * 2;
      map.flyToBounds([[c.lat - r, c.lng - r], [c.lat + r, c.lng + r]], { animate: !still, duration: 0.6 });
      return;
    }
    map.flyTo(latlng, Math.max(map.getZoom(), Math.log2(2 / a)), { animate: !still, duration: 0.6 }); // 2 px/m
  }

  function tooltip(title, lines) {
    var d = document.createElement('div');
    d.appendChild(mk('strong', null, title));
    (lines || []).forEach(function (l) { d.appendChild(mk('span', null, l)); });
    return d;
  }
  // named gives an icon an accessible name; the visible tooltip is separate.
  function named(m, text) {
    function set() { var n = m.getElement(); n.setAttribute('role', 'button'); n.setAttribute('aria-label', text()); }
    m.on('add', set);
  }
  function bindTip(layer, title, lines) {
    if (layer.getTooltip()) { layer.setTooltipContent(tooltip(title, lines)); return; }
    layer.bindTooltip(tooltip(title, lines), { className: 'tt', direction: 'right', offset: [16, 0], opacity: 1 });
  }

  // ---- context menu ----
  //
  // A menu is a spec: {title, sub, items | fields+submit+run, back, dismiss}.
  // An item either runs an action, or has a view() that returns the next spec,
  // which replaces the menu content in place. Items need a minimum role.

  var ctx = mk('div', 'ctx');
  ctx.setAttribute('role', 'menu');
  ctx.hidden = true;
  el.appendChild(ctx);
  L.DomEvent.disableClickPropagation(ctx);
  L.DomEvent.disableScrollPropagation(ctx);
  var cur = null;
  var opener = null;

  function openMenu(pt, spec) {
    closeMenu();
    opener = document.activeElement;
    cur = { pt: pt, done: false, dismiss: spec.dismiss };
    show(spec);
  }
  function closeMenu() {
    if (!cur) { return; }
    var c = cur;
    cur = null;
    ctx.hidden = true;
    ctx.textContent = '';
    if (!c.done && c.dismiss) { c.dismiss(); }
    if (opener && opener.isConnected && ctx.contains(document.activeElement)) { opener.focus(); }
  }
  function finish() { cur.done = true; closeMenu(); }

  function show(spec) {
    ctx.textContent = '';
    var h = mk('div', 'ctx-h');
    h.appendChild(mk('strong', null, spec.title));
    if (spec.sub) { h.appendChild(mk('span', null, spec.sub)); }
    ctx.appendChild(h);
    if (spec.text) { ctx.appendChild(mk('p', null, spec.text)); }
    if (spec.fields || spec.run) { ctx.appendChild(formEl(spec)); }
    if (spec.items) { itemsEl(spec); }
    if (spec.back) { ctx.appendChild(mk('hr')); ctx.appendChild(itemBtn({ label: 'Back', back: false, view: function () { return spec.back; } }, spec)); }
    ctx.hidden = false;
    place();
    var first = ctx.querySelector('input, select, [role=menuitem], button');
    if (first) { first.focus(); }
  }
  function place() {
    var W = el.clientWidth, H = el.clientHeight;
    ctx.style.left = '0px';
    ctx.style.top = '0px';
    var w = ctx.offsetWidth, hh = ctx.offsetHeight;
    ctx.style.left = Math.max(4, Math.min(cur.pt.x, W - w - 4)) + 'px';
    ctx.style.top = Math.max(4, Math.min(cur.pt.y, H - hh - 4)) + 'px';
  }

  function itemBtn(it, spec) {
    var b = mk('button', it.danger ? 'danger' : '', it.label);
    b.type = 'button';
    b.setAttribute('role', 'menuitem');
    b.addEventListener('click', function () {
      if (it.view) { var next = it.view(); next.back = it.back === false ? null : (next.back === undefined ? spec : next.back); show(next); return; }
      finish();
      it.run();
    });
    return b;
  }
  function itemsEl(spec) {
    var list = spec.items.filter(function (it) { return it === '-' || can(it.min); });
    var filter = null;
    if (spec.filter) {
      filter = mk('input', 'filter');
      filter.type = 'search';
      filter.placeholder = 'Find a player';
      filter.setAttribute('aria-label', 'Find a player');
      ctx.appendChild(filter);
    }
    var btns = [];
    list.forEach(function (it) {
      if (it === '-') { ctx.appendChild(mk('hr')); return; }
      var b = itemBtn(it, spec);
      b.dataset.q = it.label.toLowerCase();
      ctx.appendChild(b);
      btns.push(b);
    });
    if (!btns.length) { ctx.appendChild(mk('p', 'mute', spec.empty || 'Nothing you may do here.')); }
    if (filter) {
      filter.addEventListener('input', function () {
        var q = filter.value.toLowerCase();
        btns.forEach(function (b) { b.hidden = b.dataset.q.indexOf(q) < 0; });
        place();
      });
    }
  }
  function formEl(spec) {
    var f = mk('form');
    var inputs = {};
    var row = null;
    (spec.fields || []).forEach(function (fd) {
      var lab = mk('label');
      lab.appendChild(mk('span', null, fd.label));
      var inp;
      if (fd.options) {
        inp = mk('select');
        fd.options.forEach(function (o) { var op = mk('option', null, o[1]); op.value = o[0]; inp.appendChild(op); });
      } else {
        inp = mk('input');
        inp.type = fd.type || 'text';
        if (fd.step) { inp.step = fd.step; }
        if (fd.min != null) { inp.min = fd.min; }
        if (fd.max != null) { inp.max = fd.max; }
        if (fd.maxlength) { inp.maxLength = fd.maxlength; }
        if (fd.placeholder) { inp.placeholder = fd.placeholder; }
        inp.autocomplete = 'off';
      }
      inp.name = fd.name;
      inp.required = !!fd.required;
      if (fd.value != null) { inp.value = fd.value; }
      inputs[fd.name] = inp;
      lab.appendChild(inp);
      if (fd.pair) {
        if (!row) { row = mk('div', 'pair'); f.appendChild(row); }
        row.appendChild(lab);
        if (row.children.length === 2) { row = null; }
      } else { f.appendChild(lab); }
      if (fd.suggest) { suggest(inp, fd.suggest); }
    });
    var btns = mk('div', 'btns');
    var cancel = mk('button', 'quiet', 'Cancel');
    cancel.type = 'button';
    cancel.addEventListener('click', closeMenu);
    var go = mk('button', spec.danger ? 'danger' : '', spec.submit || 'OK');
    btns.appendChild(cancel);
    btns.appendChild(go);
    f.appendChild(btns);
    f.addEventListener('submit', function (e) {
      e.preventDefault();
      var v = {};
      Object.keys(inputs).forEach(function (n) { v[n] = inputs[n].value; });
      finish();
      spec.run(v);
    });
    return f;
  }
  // suggest fills a datalist with class names from the instance as the user types.
  var dlCount = 0;
  function suggest(inp, path) {
    var dl = mk('datalist');
    dl.id = 'dl' + (++dlCount);
    inp.setAttribute('list', dl.id);
    inp.parentNode.appendChild(dl);
    var t = 0;
    inp.addEventListener('input', function () {
      clearTimeout(t);
      t = setTimeout(function () {
        fetch(base + path + '?type=' + encodeURIComponent(inp.value)).then(function (r) { return r.text(); }).then(function (html) {
          var doc = new DOMParser().parseFromString('<select>' + html + '</select>', 'text/html');
          dl.textContent = '';
          doc.querySelectorAll('option').forEach(function (o) { var n = mk('option'); n.value = o.value; dl.appendChild(n); });
        });
      }, 300);
    });
  }

  ctx.addEventListener('keydown', function (e) {
    if (e.key === 'Escape') { e.stopPropagation(); closeMenu(); return; }
    if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') { return; }
    var items = Array.prototype.slice.call(ctx.querySelectorAll('input.filter, [role=menuitem]:not([hidden])'));
    var i = items.indexOf(document.activeElement);
    if (i < 0) { return; }
    e.preventDefault();
    items[(i + (e.key === 'ArrowDown' ? 1 : items.length - 1)) % items.length].focus();
  });
  document.addEventListener('pointerdown', function (e) { if (cur && !ctx.contains(e.target)) { closeMenu(); } }, true);
  map.on('movestart zoomstart', closeMenu);
  el.addEventListener('contextmenu', function (e) { e.preventDefault(); });

  // where to anchor a menu for an event: the pointer, or the layer if the
  // menu was opened from the keyboard.
  function anchor(e, latlng) {
    var oe = e.originalEvent;
    if (oe && (oe.clientX || oe.clientY)) { return e.containerPoint; }
    return map.latLngToContainerPoint(latlng || e.latlng).add([12, 0]);
  }
  function onMenu(layer, build) {
    function go(e) {
      if (e.originalEvent) { L.DomEvent.preventDefault(e.originalEvent); }
      L.DomEvent.stopPropagation(e);
      var at = e.target.getLatLng ? e.target.getLatLng() : e.latlng;
      openMenu(anchor(e, at), build());
    }
    layer.on('contextmenu', go);
    layer.on('click', go);
  }

  // ---- menus per kind ----

  var playerList = [];
  var byId = {};

  function playerPicker(title, sub, pick, exclude) {
    return {
      title: title, sub: sub, filter: true, empty: 'Nobody else is online.',
      items: playerList.filter(function (p) { return p.steam_id !== exclude; }).map(function (p) {
        return { label: p.name, min: 'operator', run: function () { pick(p); } };
      })
    };
  }

  function playerSpec(p) {
    var id = encodeURIComponent(p.steam_id);
    var here = { x: p.x, z: p.z };
    return {
      title: p.name, sub: p.steam_id + '  ' + posText(p),
      items: [
        { label: 'Message…', min: 'moderator', view: function () {
          return { title: 'Message ' + p.name, submit: 'Send', fields: [
            { name: 'text', label: 'Text', required: true, maxlength: 400 },
            { name: 'style', label: 'Shown as', options: [['chat', 'chat'], ['important', 'important'], ['notification', 'on screen']] }
          ], run: function (v) { act('/players/' + id + '/message', v); } };
        } },
        { label: 'Teleport to coordinates…', min: 'operator', view: function () {
          return { title: 'Teleport ' + p.name, submit: 'Teleport', fields: [
            { name: 'x', label: 'x (east)', type: 'number', step: 'any', required: true, value: r1(p.x), pair: true },
            { name: 'z', label: 'z (north)', type: 'number', step: 'any', required: true, value: r1(p.z), pair: true }
          ], run: function (v) { teleport(p, { x: v.x, z: v.z }); } };
        } },
        { label: 'Bring another player here…', min: 'operator', view: function () {
          return playerPicker('Bring to ' + p.name, 'Pick the player to move', function (o) {
            act('/players/' + encodeURIComponent(o.steam_id) + '/teleport', { to: p.steam_id });
          }, p.steam_id);
        } },
        { label: 'Give item…', min: 'operator', view: function () {
          return { title: 'Give to ' + p.name, submit: 'Give', fields: [
            { name: 'type', label: 'Class name', required: true, suggest: '/types' },
            { name: 'quantity', label: 'Quantity', type: 'number', step: 'any', min: 0, pair: true },
            { name: 'health', label: 'Health 0 to 1', type: 'number', step: '0.1', min: 0, max: 1, pair: true },
            { name: 'target', label: 'Into', options: [['inventory', 'inventory'], ['hands', 'hands'], ['ground', 'ground']] }
          ], run: function (v) { act('/players/' + id + '/give', v); } };
        } },
        '-',
        { label: 'Zoom to player', run: function () { zoomTo(ll(here)); } },
        { label: 'Copy Steam ID', run: function () { copy(p.steam_id); } },
        { label: 'Copy position', run: function () { copy(Math.round(p.x) + ' ' + Math.round(p.z)); } }
      ]
    };
  }

  function vehicleSpec(v) {
    var id = encodeURIComponent(v.id);
    var crew = (v.occupants || []).length;
    function repair(scope) { return function () { act('/vehicles/' + id + '/repair', { scope: scope }); }; }
    return {
      title: v.type + (v.ruined ? ' (ruined)' : ''),
      sub: 'health ' + Math.round(v.health * 100) + '%  fuel ' + Math.round(v.fuel * 100) + '%  crew ' + crew + '\n' + posText(v),
      items: [
        { label: 'Repair everything (refuel, add parts)', min: 'operator', run: repair('all') },
        { label: 'Repair only…', min: 'operator', view: function () {
          return { title: 'Repair ' + v.type, items: [['engine', 'Body'], ['parts', 'Parts'], ['wheels', 'Wheels'], ['fluids', 'Refuel'], ['missing', 'Add missing parts']].map(function (s) {
            return { label: s[1], min: 'operator', run: repair(s[0]) };
          }) };
        } },
        { label: 'Flip upright', min: 'operator', run: function () { act('/vehicles/' + id + '/flip', {}); } },
        { label: 'Delete…', min: 'operator', danger: true, view: function () {
          return { title: 'Delete ' + v.type + '?', text: crew ? crew + ' inside will be removed with it.' : 'It is removed from the world.', submit: 'Delete', danger: true,
            run: function () { act('/vehicles/' + id + '/delete', crew ? { force: '1' } : {}); } };
        } },
        '-',
        { label: 'Zoom to vehicle', run: function () { zoomTo(ll(v)); } },
        { label: 'Copy position', run: function () { copy(Math.round(v.x) + ' ' + Math.round(v.z)); } },
        { label: 'Copy vehicle ID', run: function () { copy(String(v.id)); } }
      ]
    };
  }

  function plainSpec(title, lines, o, radius) {
    return { title: title, sub: lines.join('\n'), items: [
      { label: 'Zoom to', run: function () { zoomTo(ll(o), radius); } },
      { label: 'Copy position', run: function () { copy(Math.round(o.x) + ' ' + Math.round(o.z)); } }
    ] };
  }

  function mapSpec(latlng) {
    var at = { x: r1(latlng.lng), z: r1(latlng.lat) };
    return {
      title: at.x + ' / ' + at.z, sub: 'x east / z north, metres',
      items: [
        { label: 'Teleport a player here…', min: 'operator', view: function () {
          return playerPicker('Teleport to ' + at.x + ' / ' + at.z, 'Pick the player to move', function (p) { teleport(p, at); });
        } },
        '-',
        { label: 'Centre here', run: function () { map.panTo(latlng, { animate: !still }); } },
        { label: 'Show the whole map', run: function () { toFit(true); } },
        { label: 'Copy coordinates', run: function () { copy(at.x + ' ' + at.z); } }
      ]
    };
  }
  map.on('contextmenu', function (e) { openMenu(e.containerPoint, mapSpec(e.latlng)); });
  map.on('click', closeMenu);

  // ---- layers ----

  // teleport moves the player and reports it; an icon that was dragged goes
  // back where it was if the game server says no.
  function teleport(p, to, revert) {
    return act('/players/' + encodeURIComponent(p.steam_id) + '/teleport', to).then(function (ok) {
      var e = pm[p.steam_id];
      if (ok && e) { e.data.x = +to.x; e.data.z = +to.z; e.m.setLatLng([+to.z, +to.x]); }
      if (!ok && revert) { revert(); }
    });
  }

  var pm = {};
  function playerState(p) {
    return (p.alive ? (p.unconscious ? 'hurt' : '') : 'dead') + (p.vehicle ? ' drive' : '') + (can('operator') ? '' : ' ro') + ' h' + Math.round(p.health / 5);
  }
  // The dot goes from red (no health) through yellow to green (full health, 100).
  function healthColor(h) {
    return 'hsl(' + Math.round(120 * Math.max(0, Math.min(100, h)) / 100) + ' 75% 42%)';
  }
  function playerIcon(p) {
    var root = mk('div', 'pm ' + playerState(p));
    root.style.setProperty('--hc', healthColor(p.health));
    root.appendChild(mk('span', 'pm-dot'));
    root.appendChild(mk('span', 'pm-l', p.name));
    return elIcon('pm-wrap', root);
  }
  function playerLines(p) { return [p.steam_id, 'health ' + Math.round(p.health) + (p.alive ? '' : ' (dead)'), 'ping ' + p.ping]; }

  function makePlayer(p) {
    var m = L.marker(ll(p), { icon: playerIcon(p), draggable: can('operator'), autoPan: true, riseOnHover: true });
    var e = { m: m, data: p, key: p.name + playerState(p), drag: null };
    var root = function () { return m.getElement() && m.getElement().firstChild; };
    m.on('dragstart', function () {
      closeMenu();
      e.drag = m.getLatLng();
      m.closeTooltip();
      m.unbindTooltip();
      if (root()) { root().classList.add('dragging'); }
    });
    m.on('drag', function () { showPos(e.data.name, m.getLatLng()); });
    m.on('dragend', function () {
      var from = e.drag;
      var at = m.getLatLng();
      var to = { x: r1(at.lng), z: r1(at.lat) };
      m.setLatLng([to.z, to.x]);
      if (root()) { root().classList.remove('dragging'); }
      e.drag = null;
      bindTip(m, e.data.name, playerLines(e.data));
      var settled = false;
      openMenu(map.latLngToContainerPoint(m.getLatLng()).add([20, -10]), {
        title: 'Teleport ' + e.data.name + '?', text: 'To ' + to.x + ' / ' + to.z, submit: 'Teleport',
        run: function () { settled = true; teleport(e.data, to, function () { m.setLatLng(from); }); },
        dismiss: function () { if (!settled) { m.setLatLng(from); } }
      });
    });
    onMenu(m, function () { return playerSpec(e.data); });
    named(m, function () { return 'Player ' + e.data.name; });
    bindTip(m, p.name, playerLines(p));
    m.addTo(players);
    return e;
  }

  function onPlayers(list) {
    playerList = list;
    st.online = list.length;
    drawStats();
    var seen = {};
    list.forEach(function (p) {
      seen[p.steam_id] = true;
      var e = pm[p.steam_id];
      if (!e) { pm[p.steam_id] = makePlayer(p); return; }
      e.data = p;
      if (e.drag) { return; }
      e.m.setLatLng(ll(p));
      var key = p.name + playerState(p);
      if (key !== e.key) { e.key = key; e.m.setIcon(playerIcon(p)); }
      bindTip(e.m, p.name, playerLines(p));
    });
    Object.keys(pm).forEach(function (id) {
      if (!seen[id]) { players.removeLayer(pm[id].m); delete pm[id]; }
    });
  }

  var vm = {};
  function vehicleIcon(v) {
    var bike = v.kind === 'motorbike';
    var glyph = iconClass(bike ? 'motorcycle' : 'car');
    var root = mk('div', 'vm' + (bike ? ' bike' : '') + (glyph ? ' glyph' : '') + (v.ruined ? ' ruined' : ''));
    if (glyph) { root.appendChild(mk('i', glyph)); }
    return elIcon('vm-wrap', root);
  }
  function vehicleLines(v) { return ['health ' + Math.round(v.health * 100) + '%', 'fuel ' + Math.round(v.fuel * 100) + '%', 'crew ' + (v.occupants || []).length]; }
  function onVehicles(list) {
    var seen = {};
    list.forEach(function (v) {
      seen[v.id] = true;
      var e = vm[v.id];
      if (!e) {
        var m = L.marker(ll(v), { icon: vehicleIcon(v) });
        e = vm[v.id] = { m: m, data: v, ruined: v.ruined };
        onMenu(m, function () { return vehicleSpec(e.data); });
        named(m, function () { return (e.data.kind === 'motorbike' ? 'Motorbike ' : 'Vehicle ') + e.data.type; });
        m.addTo(vehicles);
      }
      e.data = v;
      e.m.setLatLng(ll(v));
      if (e.ruined !== v.ruined) { e.ruined = v.ruined; e.m.setIcon(vehicleIcon(v)); }
      bindTip(e.m, v.type, vehicleLines(v));
    });
    Object.keys(vm).forEach(function (id) {
      if (!seen[id]) { vehicles.removeLayer(vm[id].m); delete vm[id]; }
    });
  }

  function onEvents(list) {
    events.clearLayers();
    list.forEach(function (e) {
      var color = markerColor(e.color);
      var glyph = iconClass(e.icon);
      var title = e.label || e.name;
      var sub = e.name !== title ? e.name : e.category;
      var m;
      if (e.radius) {
        // An area (a contaminated zone): the circle, and the icon in its middle.
        L.circle(ll(e), { radius: e.radius, className: 'ev', color: color, fillColor: color }).addTo(events);
      }
      if (glyph) {
        m = L.marker(ll(e), { icon: markerIcon(glyph, color) });
        named(m, function () { return title; });
      } else {
        m = L.circleMarker(ll(e), { radius: 5, color: color, fillOpacity: 0.9 });
      }
      bindTip(m, title, [sub]);
      onMenu(m, function () { return plainSpec(title, [sub, posText(e)], e, e.radius || 50); });
      m.addTo(events);
    });
  }

  // ---- marker icons ----
  //
  // A marker's icon is the name of a Fork Awesome icon (fire, car, ...), from Debian's
  // fonts-fork-awesome. A name the font does not know, or a font that is not there, leaves the
  // marker a coloured dot.
  var glyphs = {};
  function iconClass(name) {
    var n = String(name || '').replace(/^fa-/, '');
    if (!/^[a-z0-9]+(-[a-z0-9]+)*$/.test(n)) { return ''; }
    if (!(n in glyphs)) {
      var probe = mk('i', 'fa fa-' + n);
      probe.style.cssText = 'position:absolute;visibility:hidden';
      document.body.appendChild(probe);
      var c = getComputedStyle(probe, '::before').content;
      glyphs[n] = c && c !== 'none' && c !== 'normal' ? 'fa fa-' + n : '';
      document.body.removeChild(probe);
    }
    return glyphs[n];
  }
  function markerColor(c) {
    return c && window.CSS && CSS.supports('color', c) ? c : '#7a3db8';
  }
  function markerIcon(cls, color) {
    var root = mk('div', 'mi');
    root.style.setProperty('--mc', color);
    root.appendChild(mk('i', cls));
    return elIcon('mi-wrap', root);
  }

  function onMarkers(list) {
    Object.keys(markerLayers).forEach(function (k) { markerLayers[k].clearLayers(); });
    list.forEach(function (m) {
      var group = markerLayers[m.layer];
      if (!group) {
        group = L.layerGroup().addTo(map);
        markerLayers[m.layer] = group;
        control.addOverlay(group, m.layer);
      }
      var color = markerColor(m.color);
      var glyph = m.shape && m.shape !== 'point' ? '' : iconClass(m.icon);
      var shape;
      if (m.shape === 'circle') {
        shape = L.circle(ll(m), { radius: m.radius || 50, color: color, weight: 2, fillOpacity: 0.15 });
      } else if ((m.shape === 'polygon' || m.shape === 'polyline') && m.points && m.points.length) {
        var pts = m.points.map(function (p) { return [p.z, p.x]; });
        shape = m.shape === 'polygon' ? L.polygon(pts, { color: color }) : L.polyline(pts, { color: color });
      } else if (glyph) {
        shape = L.marker(ll(m), { icon: markerIcon(glyph, color) });
        named(shape, function () { return (m.label || m.id) + ' (' + m.layer + ')'; });
      } else {
        shape = L.circleMarker(ll(m), { radius: 5, color: color, fillOpacity: 0.9 });
      }
      var lines = (m.props || []).map(function (kv) { return kv.k + ': ' + kv.v; });
      var title = m.label || m.id;
      bindTip(shape, title, lines);
      onMenu(shape, function () { return plainSpec(title, lines.concat(posText(m)), m, m.shape === 'circle' ? m.radius : 0); });
      shape.addTo(group);
    });
  }

  // ---- statistics in the title: players online, uptime, next scheduled restart ----

  var stats = document.getElementById('stats');
  var st = { clock: '', online: null, since: null, next: null, connected: true };
  function span(d) {
    if (d < 60) { return '<1m'; }
    var m = Math.floor(d / 60) % 60, h = Math.floor(d / 3600) % 24, days = Math.floor(d / 86400);
    return days ? days + 'd ' + h + 'h' : h ? h + 'h ' + m + 'm' : m + 'm';
  }
  function drawStats() {
    if (!stats) { return; }
    var now = Date.now();
    var up = st.connected && st.since ? span((now - st.since) / 1000) : '';
    var nx = '';
    if (st.next) {
      nx = st.next <= now ? 'now' : 'in ' + span((st.next - now) / 1000);
      stats.querySelector('.st-restart').title = new Date(st.next).toLocaleString();
    }
    var ck = stats.querySelector('.st-clock');
    ck.hidden = !st.connected || !st.clock;
    ck.lastChild.textContent = st.clock;
    var hr = parseInt(st.clock, 10);
    ck.firstChild.className = 'fa fa-' + (hr >= 6 && hr < 18 ? 'sun-o' : 'moon-o');
    [['.st-players', st.online === null ? '' : st.online + ' online'], ['.st-up', up && 'up ' + up], ['.st-restart', nx && 'restart ' + nx]].forEach(function (x) {
      var s = stats.querySelector(x[0]);
      s.textContent = x[1];
      s.hidden = !x[1];
    });
  }
  function onStatus(s) {
    st.connected = !!s.connected;
    st.clock = s.game_time || '';
    st.since = s.hello && s.hello.since ? Date.parse(s.hello.since) : null;
    st.next = s.next_restart ? Date.parse(s.next_restart) : null;
    if (st.online === null) { st.online = s.players; }
    drawStats();
  }
  setInterval(drawStats, 30000);

  var handlers = { players: onPlayers, vehicles: onVehicles, events: onEvents, markers: onMarkers, status: onStatus };
  var es = new EventSource(el.dataset.stream);
  es.addEventListener('error', function () { document.querySelector('main').classList.add('stale'); });
  Object.keys(handlers).forEach(function (kind) {
    es.addEventListener(kind, function (e) {
      document.querySelector('main').classList.remove('stale');
      handlers[kind](JSON.parse(e.data) || []);
    });
  });
})();
