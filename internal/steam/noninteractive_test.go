// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package steam

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNonInteractivePrompterRefusesPassword(t *testing.T) {
	var p NonInteractivePrompter
	_, err := p.Password(t.Context(), "bob")
	assert.True(t, errors.Is(err, ErrAuthRequired), "err = %v, want ErrAuthRequired", err)
}

func TestNonInteractivePrompterRefusesCode(t *testing.T) {
	var p NonInteractivePrompter
	_, err := p.Code(t.Context(), PromptGuardCode)
	assert.True(t, errors.Is(err, ErrAuthRequired), "err = %v, want ErrAuthRequired", err)
}

func TestNonInteractivePrompterWaitingIsNoop(t *testing.T) {
	var p NonInteractivePrompter
	p.Waiting(t.Context()) // must not panic or block
}

func TestRunWithNonInteractivePrompterReportsAuthRequired(t *testing.T) {
	script := writeFakeSteamcmd(t, `
printf 'password: '
read pass
echo "Waiting for user info...OK"
`)
	orig := killGraceDelay
	killGraceDelay = 100 * time.Millisecond
	defer func() { killGraceDelay = orig }()

	_, err := Run(t.Context(), runOpts(t, script, NonInteractivePrompter{}))
	assert.True(t, errors.Is(err, ErrAuthRequired), "err = %v, want ErrAuthRequired", err)
}
