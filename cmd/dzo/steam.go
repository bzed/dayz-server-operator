// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/creack/pty"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/steam"
)

func newSteamCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "steam",
		Short: "Interactive steamcmd login and session status (§C7)",
	}
	cmd.AddCommand(newSteamLoginCmd(), newSteamStatusCmd())
	return cmd
}

func newSteamLoginCmd() *cobra.Command {
	var user, configPath, statusFile, command string
	var timeout time.Duration
	var passthrough bool
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Log steamcmd into Steam interactively (password + Steam Guard)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if passthrough {
				return runPassthrough(cmd, command, user)
			}
			return runSteamLogin(cmd, steamLoginParams{
				user: user, configPath: configPath, statusFile: statusFile,
				command: command, timeout: timeout,
			})
		},
	}
	cmd.Flags().StringVar(&user, "user", "", "Steam account name (required)")
	cmd.Flags().StringVar(&configPath, "config", defaultConfigPath, "path to config.yaml (used to derive --status-file when unset)")
	cmd.Flags().StringVar(&statusFile, "status-file", "", "where to persist login status (default: <paths.secrets>/steam-status.json)")
	cmd.Flags().StringVar(&command, "command", "steamcmd", "steamcmd binary to run")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "overall timeout for the whole interactive login")
	cmd.Flags().BoolVar(&passthrough, "passthrough", false,
		"skip the prompt state machine and connect this terminal directly to steamcmd's pty (fallback if Steam changes its prompts)")
	_ = cmd.MarkFlagRequired("user")
	return cmd
}

type steamLoginParams struct {
	user, configPath, statusFile, command string
	timeout                               time.Duration
}

func runSteamLogin(cmd *cobra.Command, p steamLoginParams) error {
	path, err := resolveStatusPath(p.configPath, p.statusFile)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), p.timeout)
	defer cancel()

	prompter := &stdinPrompter{in: cmd.InOrStdin(), out: cmd.OutOrStdout()}
	result, runErr := steam.Run(ctx, steam.Options{Command: p.command, Account: p.user, Prompter: prompter})
	if runErr == nil && !result.Success && result.Reason == steam.FailureUnknown {
		// steamcmd ended with output the marker table does not know, which says nothing about the
		// login itself: it may well have worked (seen with a Steam Guard code). Ask the session it
		// cached, without prompting; only that answer counts.
		recheck, recheckErr := steam.Run(ctx, steam.Options{Command: p.command, Account: p.user, Prompter: steam.NonInteractivePrompter{}})
		if recheckErr == nil && recheck.Success {
			result = recheck
		} else {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "unrecognised steamcmd output:\n%s\n", result.Log)
		}
	}

	status, loadErr := steam.LoadStatus(path)
	if loadErr != nil {
		return loadErr
	}
	status.Record(p.user, result, time.Now())
	if saveErr := status.Save(path); saveErr != nil {
		return saveErr
	}

	if runErr != nil {
		return fmt.Errorf("steam login: %w", runErr)
	}
	if !result.Success {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "login failed: %s\n", result.Reason)
		return fmt.Errorf("steam login: %s", result.Reason)
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), "login OK")
	return nil
}

func newSteamStatusCmd() *cobra.Command {
	var configPath, statusFile string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the last known Steam login state",
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := resolveStatusPath(configPath, statusFile)
			if err != nil {
				return err
			}
			status, err := steam.LoadStatus(path)
			if err != nil {
				return err
			}
			printSteamStatus(cmd, status)
			return nil
		},
	}
	cmd.Flags().StringVar(&configPath, "config", defaultConfigPath, "path to config.yaml (used to derive --status-file when unset)")
	cmd.Flags().StringVar(&statusFile, "status-file", "", "where login status is persisted (default: <paths.secrets>/steam-status.json)")
	return cmd
}

func printSteamStatus(cmd *cobra.Command, s steam.Status) {
	out := cmd.OutOrStdout()
	if s.Account == "" {
		_, _ = fmt.Fprintln(out, "no login recorded yet")
		return
	}
	_, _ = fmt.Fprintf(out, "account:       %s\n", s.Account)
	_, _ = fmt.Fprintf(out, "auth required: %t\n", s.AuthRequired)
	if s.LastSuccessAt != nil {
		_, _ = fmt.Fprintf(out, "last success:  %s\n", s.LastSuccessAt.Format(time.RFC3339))
	}
	if s.LastFailureAt != nil {
		_, _ = fmt.Fprintf(out, "last failure:  %s (%s)\n", s.LastFailureAt.Format(time.RFC3339), s.LastFailureReason)
	}
}

func resolveStatusPath(configPath, statusFile string) (string, error) {
	if statusFile != "" {
		return statusFile, nil
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return "", fmt.Errorf("resolve steam status path (pass --status-file to skip config.yaml): %w", err)
	}
	return filepath.Join(cfg.Paths.Secrets, "steam-status.json"), nil
}

// stdinPrompter answers steamcmd's interactive prompts by reading from an
// io.Reader (normally the process's stdin). It puts the underlying
// descriptor into no-echo mode for password/code entry when it is a real
// terminal, and falls back to plain buffered line reads otherwise (piped
// input in tests). Reads are blocking, like any other interactive CLI
// prompt; they are not cancelled early if ctx expires while waiting on a
// human to type.
type stdinPrompter struct {
	in  io.Reader
	out io.Writer

	buffered *bufio.Reader // lazily created, then reused: a fresh bufio.Reader per call would silently drop everything it had already read ahead past the first line
}

func (p *stdinPrompter) Password(_ context.Context, account string) (string, error) {
	_, _ = fmt.Fprintf(p.out, "steamcmd password for %s: ", account)
	line, err := p.readLine()
	_, _ = fmt.Fprintln(p.out)
	return line, err
}

func (p *stdinPrompter) Code(_ context.Context, kind steam.PromptKind) (string, error) {
	_, _ = fmt.Fprintf(p.out, "Steam %s: ", kind)
	line, err := p.readLine()
	_, _ = fmt.Fprintln(p.out)
	return line, err
}

func (p *stdinPrompter) Waiting(_ context.Context) {
	_, _ = fmt.Fprintln(p.out, "confirm this login in the Steam mobile app; waiting...")
}

func (p *stdinPrompter) readLine() (string, error) {
	if f, ok := p.in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		data, err := term.ReadPassword(int(f.Fd()))
		if err != nil {
			return "", fmt.Errorf("read from terminal: %w", err)
		}
		return string(data), nil
	}
	if p.buffered == nil {
		p.buffered = bufio.NewReader(p.in)
	}
	line, err := p.buffered.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read input: %w", err)
	}
	return trimNewline(line), nil
}

func trimNewline(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

// runPassthrough skips the prompt state machine entirely and wires this
// process's own terminal directly to steamcmd's pty, as a fallback for
// when Steam's prompts have changed in a way the classifier in
// internal/steam does not recognise (§C7).
func runPassthrough(cmd *cobra.Command, command, user string) error {
	c := exec.CommandContext(cmd.Context(), command, "+login", user) //nolint:gosec // command/user are operator-supplied CLI flags
	ptmx, err := pty.Start(c)
	if err != nil {
		return fmt.Errorf("steam: start %s: %w", command, err)
	}
	defer func() { _ = ptmx.Close() }()

	if f, ok := cmd.InOrStdin().(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		oldState, err := term.MakeRaw(int(f.Fd()))
		if err == nil {
			defer func() { _ = term.Restore(int(f.Fd()), oldState) }()
		}
	}

	go func() { _, _ = io.Copy(ptmx, cmd.InOrStdin()) }()
	_, _ = io.Copy(cmd.OutOrStdout(), ptmx)
	return c.Wait()
}
