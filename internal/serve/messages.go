// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"sync"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/battleye"
	"github.com/bzed/dayz-server-operator/internal/broadcast"
	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/resolve"
	"github.com/bzed/dayz-server-operator/internal/runfiles"
	"github.com/bzed/dayz-server-operator/internal/site"
)

// instanceMessages is what an instance sends: the welcome, then its messages.
func instanceMessages(inst *resolve.Instance) []site.Message {
	var ms []site.Message
	if w, ok := site.WelcomeMessage(inst.Welcome); ok {
		ms = append(ms, w)
	}
	return append(ms, inst.Messages...)
}

// messengers are the running broadcasts of the instances.
type messengers struct {
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func (m *messengers) stop() {
	m.cancel()
	m.wg.Wait()
}

// startMessengers starts the server messages of every instance that has any: through the
// dzo-admin mod when the instance runs it, else over RCon.
func startMessengers(ctx context.Context, cfg *config.Config, tree *site.Tree, reg *admin.Registry, logf func(string, ...any)) (*messengers, error) {
	all, err := resolve.ResolveAll(cfg, tree)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	ms := &messengers{cancel: cancel}
	for _, inst := range all {
		msgs := instanceMessages(inst)
		if len(msgs) == 0 {
			continue
		}
		var backend broadcast.Backend
		if hub, ok := reg.Hub(inst.Name); ok && inst.AdminEnabled() {
			backend = &broadcast.Mod{Hub: hub}
		} else {
			pw, err := runfiles.RConPassword(cfg.Paths.Secrets, inst.Name)
			if err != nil {
				logf("messages of %s: %v", inst.Name, err)
				continue
			}
			s := &battleye.Session{Addr: inst.RConAddr(), Password: pw}
			s.Start()
			ms.wg.Add(1)
			go func() { // the session ends with the messenger
				defer ms.wg.Done()
				<-ctx.Done()
				_ = s.Close()
			}()
			backend = &broadcast.RCon{Session: s}
		}
		name := inst.Name
		m := &broadcast.Messenger{
			Backend: backend, Messages: msgs, Name: name, Port: inst.Ports.Game,
			Log: func(format string, a ...any) { logf("messages of %s: "+format, append([]any{name}, a...)...) },
		}
		ms.wg.Add(1)
		go func() {
			defer ms.wg.Done()
			m.Run(ctx)
		}()
	}
	return ms, nil
}
