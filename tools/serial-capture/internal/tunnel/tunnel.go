// Package tunnel builds the reverse-SSH-tunnel command that exposes the local
// capture port on a remote host, so a remote agent can read the console. It
// wraps the system `ssh` (respecting the user's keys, config, known_hosts and
// password prompt) rather than reimplementing SSH.
package tunnel

import "fmt"

// SSHArgs builds the argument list for a reverse-tunnel ssh invocation:
// forward `remotePort` on `target` back to `127.0.0.1:localPort` on this
// machine. ExitOnForwardFailure makes a failed remote bind a hard error (loud,
// not a silent no-op); the keepalives detect and drop a dead link. Both ends are
// pinned to IPv4 127.0.0.1 to avoid localhost IPv4/IPv6 mismatches.
func SSHArgs(target string, remotePort, localPort int) []string {
	return []string{
		"-N",
		"-o", "ExitOnForwardFailure=yes",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
		"-R", fmt.Sprintf("%d:127.0.0.1:%d", remotePort, localPort),
		target,
	}
}
