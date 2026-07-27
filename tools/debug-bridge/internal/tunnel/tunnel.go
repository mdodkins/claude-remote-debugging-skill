// Package tunnel builds the reverse-SSH-tunnel command that exposes the local
// capture port on a remote host, so a remote agent can read the console. It
// wraps the system `ssh` (respecting the user's keys, config, known_hosts and
// password prompt) rather than reimplementing SSH.
package tunnel

import "fmt"

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
