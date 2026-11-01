# SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
# SPDX-License-Identifier: AGPL-3.0-or-later

# Sphinx configuration for the dzo documentation.
# Only Sphinx itself (python3-sphinx) is needed; no extensions, built-in theme.

project = "dzo"
copyright = "2026 Bernd Zeimetz"
author = "Bernd Zeimetz"
release = "0.0"

extensions = []
exclude_patterns = ["_build"]
language = "en"

html_theme = "alabaster"
html_title = "dzo documentation"
html_theme_options = {
    "description": "DayZ server operator",
    "fixed_sidebar": True,
    "show_relbars": True,
}
html_favicon = "../internal/web/static/favicon.svg"
html_show_sourcelink = False
