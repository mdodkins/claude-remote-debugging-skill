// Package tunnel builds the reverse-SSH-tunnel command that exposes the local
// capture port on a remote host, so a remote agent can read the console. It
// wraps the system `ssh` (respecting the user's keys, config, known_hosts and
// password prompt) rather than reimplementing SSH.
package tunnel

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// SSHArgs builds the argument list for a reverse-tunnel ssh invocation that
// forwards each of `ports` on `target` back to the same port on 127.0.0.1 here
// — one -R per port, so the serial console and the J-Link GDB port can share a
// single tunnel. ExitOnForwardFailure makes a failed remote bind a hard error
// (loud, not a silent no-op); the keepalives detect and drop a dead link. Both
// ends are pinned to IPv4 127.0.0.1 to avoid localhost IPv4/IPv6 mismatches.
func SSHArgs(target string, ports ...int) []string {
	args := []string{
		"-N",
		"-o", "ExitOnForwardFailure=yes",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
	}
	for _, p := range ports {
		args = append(args, "-R", fmt.Sprintf("%d:127.0.0.1:%d", p, p))
	}
	return append(args, target)
}

// ForwardFailed reports whether a line of ssh's stderr says a remote port
// could not be bound. With ExitOnForwardFailure that is why ssh exited, and the
// usual cause is the server's sshd still holding the port for this user's
// previous session, whose network went away without a clean close (a laptop
// switching from one uplink to another, a dongle unplugged). sshd only notices
// a dead client when its own keepalive gives up, which by default is hours.
func ForwardFailed(line string) bool {
	return strings.Contains(line, "remote port forwarding failed")
}

// EvictArgs builds an ssh invocation that frees `ports` on `target` by ending
// the stale session holding them. It runs as the same user, so `ss -p` only
// shows that user's own processes, and it kills only a process named sshd: a
// forward that this user's dead session left behind, never anything else that
// happens to listen there.
func EvictArgs(target string, ports ...int) []string {
	return []string{"-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3", target, EvictScript(ports...)}
}

// EvictScript is the remote shell snippet EvictArgs runs. It prints one line
// per port it freed, so the bridge can log what it did.
func EvictScript(ports ...int) string {
	ps := make([]string, len(ports))
	for i, p := range ports {
		ps[i] = strconv.Itoa(p)
	}
	return "for p in " + strings.Join(ps, " ") + "; do " +
		`for pid in $(ss -Hltnp "sport = :$p" 2>/dev/null | grep -o 'pid=[0-9]*' | cut -d= -f2 | sort -u); do ` +
		`[ "$(ps -o comm= -p "$pid")" = sshd ] && kill "$pid" && echo "freed port $p (stale sshd pid $pid)"; ` +
		"done; done; true"
}

// Backoff returns the wait before reconnect attempt n (0-based): 2s, 4s, 8s,
// 16s, then 30s from there on.
func Backoff(n int) time.Duration {
	if n >= 4 {
		return 30 * time.Second
	}
	return time.Duration(2<<uint(n)) * time.Second
}
