#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Build dzo-cheatsheet.pdf (two A4 pages) from template.html with headless Chrome.

The contour lines of the masthead are generated here (fixed seed, so the PDF is stable).
Fonts: IBM Plex Sans, IBM Plex Sans Condensed and IBM Plex Mono must be installed.
"""
import math
import os
import random
import subprocess
import sys

here = os.path.dirname(os.path.abspath(__file__))
random.seed(7)


def contour(cx, cy, r, rot):
    ph = [random.uniform(0, 6.28) for _ in range(3)]
    pts = []
    for i in range(48):
        a = 2 * math.pi * i / 48
        k = 1 + 0.10 * math.sin(2 * a + ph[0]) + 0.06 * math.sin(3 * a + ph[1]) + 0.03 * math.sin(5 * a + ph[2])
        x, y = r * 1.35 * k * math.cos(a), r * 0.85 * k * math.sin(a)
        pts.append((cx + x * math.cos(rot) - y * math.sin(rot), cy + x * math.sin(rot) + y * math.cos(rot)))
    return "M" + " L".join(f"{x:.1f},{y:.1f}" for x, y in pts) + "Z"


paths = "".join(f'<path d="{contour(300, 120, r, -0.35)}"/>' for r in range(18, 150, 13))
svg = ('<svg class="topo" viewBox="0 0 420 200" aria-hidden="true" preserveAspectRatio="xMaxYMid slice">'
       f'<g fill="none" stroke="currentColor" stroke-width="0.9">{paths}</g></svg>')
html = open(os.path.join(here, "template.html"), encoding="utf-8").read().replace("%TOPO%", svg)
out_html = os.path.join(here, "dzo-cheatsheet.html")
open(out_html, "w", encoding="utf-8").write(html)
chrome = os.environ.get("CHROME", "google-chrome")
pdf = os.path.join(here, "dzo-cheatsheet.pdf")
subprocess.run([chrome, "--headless=new", "--no-sandbox", "--disable-gpu", "--no-pdf-header-footer",
                f"--print-to-pdf={pdf}", "file://" + out_html], check=True, stderr=subprocess.DEVNULL)
os.remove(out_html)
print("wrote", pdf, file=sys.stderr)
