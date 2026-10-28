// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"

	"github.com/bzed/dayz-server-operator/internal/api"
)

// can is what the person of a request may do on one instance. The pages show a control only when its flag is
// true; the API checks every action again, so a forged request still gets its 403.
type can struct {
	Message, Broadcast, Kick, Kill, Heal, FixGear, Teleport, Spawn, Repair, Delete, Raise, BanInstance, EvidenceAdd bool
}

// Any reports whether the person may do anything on the instance.
func (c can) Any() bool {
	return c.Message || c.Broadcast || c.Kick || c.Kill || c.Heal || c.FixGear || c.Teleport || c.Spawn || c.Repair || c.Delete || c.Raise || c.BanInstance
}

// Help is true when the person may heal or fix gear.
func (c can) Help() bool { return c.Heal || c.FixGear }

func canOf(role api.Role) can {
	return can{
		Message: role.Can(api.PermMessage), Broadcast: role.Can(api.PermBroadcast), Kick: role.Can(api.PermKick),
		Kill: role.Can(api.PermKill), Heal: role.Can(api.PermHeal), FixGear: role.Can(api.PermFixGear),
		Teleport: role.Can(api.PermTeleport), Spawn: role.Can(api.PermSpawn), Repair: role.Can(api.PermRepair),
		Delete: role.Can(api.PermDelete), Raise: role.Can(api.PermRaise), BanInstance: role.Can(api.PermBanInstance),
		EvidenceAdd: role.Can(api.PermEvidenceAdd),
	}
}

// infoOf is what GET /api/v1 said about the person of a request (at most a few seconds old).
func infoOf(r *http.Request) api.Info {
	if d := sessionFrom(r.Context()); d != nil {
		return d.info
	}
	return api.Info{}
}

// roleOn is the person's effective role on an instance, "" for none.
func roleOn(info api.Info, instance string) api.Role {
	if info.User != nil {
		return info.User.Roles[instance]
	}
	return info.Role
}

// canOn is what the person may do on an instance.
func canOn(r *http.Request, instance string) can { return canOf(roleOn(infoOf(r), instance)) }

// canAnywhere is what the person may do on at least one instance.
func canAnywhere(r *http.Request) can {
	info := infoOf(r)
	if info.User == nil {
		return canOf(info.Role)
	}
	var all can
	for _, role := range info.User.Roles {
		c := canOf(role)
		all.BanInstance = all.BanInstance || c.BanInstance
		all.EvidenceAdd = all.EvidenceAdd || c.EvidenceAdd
	}
	return all
}

// holds reports whether the person has an installation-wide permission (users.manage, audit.view, ...).
func holds(r *http.Request, perm string) bool {
	info := infoOf(r)
	if info.User == nil {
		return info.Role.Can(perm)
	}
	for _, p := range info.User.Installation {
		if p == perm {
			return true
		}
	}
	return false
}
