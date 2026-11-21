// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package supervise

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

// notifyTimeout bounds one notification. systemd reads its notification
// socket continuously: a datagram that cannot be queued for this long means
// nobody is reading, and the unit's start timeout reports the missing READY
// anyway.
const notifyTimeout = time.Second

// notifyRetry is the pause between two tries while the receiver's queue is full.
const notifyRetry = 10 * time.Millisecond

// SdNotify implements sd_notify(3): it sends state to the systemd notification
// socket named by $NOTIFY_SOCKET, and does nothing when the variable is unset
// (the supervisor also runs outside systemd, in tests and by hand).
//
// $NOTIFY_SOCKET is a unix datagram socket; a leading "@" is systemd's
// notation for the abstract namespace, which syscall.SockaddrUnix understands
// as it is. Every send is MSG_DONTWAIT on a non-blocking socket, so a receiver
// that does not read can never block the supervisor: a full queue is retried
// until notifyTimeout and then reported. (A blocking write was seen to hang a
// CI test for ten minutes despite a write deadline.)
func SdNotify(state string) error {
	socket := os.Getenv("NOTIFY_SOCKET")
	if socket == "" {
		return nil
	}
	fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_DGRAM|syscall.SOCK_CLOEXEC|syscall.SOCK_NONBLOCK, 0)
	if err != nil {
		return fmt.Errorf("sd_notify: %w", err)
	}
	defer func() { _ = syscall.Close(fd) }() // a datagram socket holds no buffered state
	to := &syscall.SockaddrUnix{Name: socket}
	deadline := time.Now().Add(notifyTimeout)
	for {
		err := syscall.Sendto(fd, []byte(state), syscall.MSG_DONTWAIT|syscall.MSG_NOSIGNAL, to)
		switch {
		case err == nil:
			return nil
		case errors.Is(err, syscall.EINTR):
			continue
		case errors.Is(err, syscall.EAGAIN) && time.Now().Before(deadline):
			time.Sleep(notifyRetry)
		default:
			return fmt.Errorf("sd_notify %s: %w", socket, err)
		}
	}
}
