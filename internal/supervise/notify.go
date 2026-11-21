// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package supervise

import (
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

// notifyTimeout bounds one datagram to systemd's notification socket. The
// socket is local and systemd reads it continuously: a send that does not
// complete quickly means nobody is listening, and the unit's start timeout
// reports the missing READY anyway.
const notifyTimeout = 5 * time.Second

// SdNotify implements sd_notify(3): it sends state to the systemd notification
// socket named by $NOTIFY_SOCKET, and does nothing when the variable is unset
// (the supervisor also runs outside systemd, in tests and by hand).
//
// $NOTIFY_SOCKET is a unix datagram socket; a leading "@" is systemd's
// notation for the abstract namespace, which is an address starting with a
// NUL byte. The socket is used as it is otherwise: an absolute path.
func SdNotify(state string) error {
	socket := os.Getenv("NOTIFY_SOCKET")
	if socket == "" {
		return nil
	}
	if strings.HasPrefix(socket, "@") {
		socket = "\x00" + socket[1:]
	}
	conn, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: socket, Net: "unixgram"})
	if err != nil {
		return fmt.Errorf("sd_notify: %w", err)
	}
	defer func() { _ = conn.Close() }() // a datagram connection holds no buffered state
	if err := conn.SetWriteDeadline(time.Now().Add(notifyTimeout)); err != nil {
		return fmt.Errorf("sd_notify: %w", err)
	}
	if _, err := conn.Write([]byte(state)); err != nil {
		return fmt.Errorf("sd_notify: %w", err)
	}
	return nil
}
