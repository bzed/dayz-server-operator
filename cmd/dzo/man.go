// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// newManCmd writes the man pages of every command (the Debian build runs it). A few lines of
// roff do the job; cobra's own generator would pull two more modules in.
func newManCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "gen-man <directory>",
		Short:  "Write the man pages of dzo and all its commands into a directory",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := os.MkdirAll(args[0], 0o755); err != nil { //nolint:gosec // man pages are world-readable
				return err
			}
			n, err := writeMan(cmd.Root(), args[0])
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%d man pages written to %s\n", n, args[0])
			return nil
		},
	}
}

func manName(c *cobra.Command) string { return strings.ReplaceAll(c.CommandPath(), " ", "-") }

// roff escapes text for a man page.
func roff(s string) string {
	s = strings.ReplaceAll(s, `\`, `\e`)
	s = strings.ReplaceAll(s, "-", `\-`)
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, ".") || strings.HasPrefix(l, "'") {
			l = `\&` + l
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

func writeMan(c *cobra.Command, dir string) (int, error) {
	if c.Hidden || c.Name() == "help" || c.Name() == "completion" {
		return 0, nil
	}
	f, err := os.Create(filepath.Join(dir, manName(c)+".1")) //nolint:gosec // a name made from command names
	if err != nil {
		return 0, err
	}
	renderMan(f, c)
	if err := f.Close(); err != nil {
		return 0, err
	}
	n := 1
	for _, sub := range c.Commands() {
		k, err := writeMan(sub, dir)
		if err != nil {
			return n, err
		}
		n += k
	}
	return n, nil
}

func renderMan(w io.Writer, c *cobra.Command) {
	p := func(format string, a ...any) { _, _ = fmt.Fprintf(w, format, a...) }
	p(".TH \"%s\" \"1\" \"\" \"dzo\" \"DayZ server operator\"\n", strings.ToUpper(manName(c)))
	p(".SH NAME\n%s \\- %s\n", roff(manName(c)), roff(c.Short))
	p(".SH SYNOPSIS\n.B %s\n", roff(c.UseLine()))
	if c.HasAvailableSubCommands() {
		p("\n.I command\n")
	}
	if long := strings.TrimSpace(c.Long); long != "" {
		p(".SH DESCRIPTION\n%s\n", roff(long))
	}
	var opts []string
	c.NonInheritedFlags().VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			return
		}
		name := "\\-\\-" + roff(f.Name)
		if f.Shorthand != "" {
			name = "\\-" + f.Shorthand + ", " + name
		}
		def := ""
		if f.DefValue != "" && f.DefValue != "false" && f.DefValue != "0" && f.DefValue != "[]" {
			def = " (default " + roff(f.DefValue) + ")"
		}
		opts = append(opts, fmt.Sprintf(".TP\n.B %s\n%s%s\n", name, roff(f.Usage), def))
	})
	if len(opts) > 0 {
		p(".SH OPTIONS\n%s", strings.Join(opts, ""))
	}
	if subs := c.Commands(); len(subs) > 0 {
		p(".SH COMMANDS\n")
		for _, s := range subs {
			if s.Hidden || s.Name() == "help" || s.Name() == "completion" {
				continue
			}
			p(".TP\n.B %s\n%s\n", roff(manName(s))+"(1)", roff(s.Short))
		}
	}
	if c.HasParent() {
		p(".SH SEE ALSO\n.BR %s (1)\n", roff(manName(c.Parent())))
	}
}
