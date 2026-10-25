package tunnel

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSSHArgsSinglePort(t *testing.T) {
	got := SSHArgs("me@shard.example.com", 5555)
	want := []string{
		"-N",
		"-o", "ExitOnForwardFailure=yes",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
		"-R", "5555:127.0.0.1:5555",
		"me@shard.example.com",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %#v\nwant %#v", got, want)
	}
}

func TestSSHArgsMultiplePortsEachGetAForward(t *testing.T) {
	// serial + GDB in one tunnel
	got := SSHArgs("user@host", 5555, 2331)

	// both ports appear as their own -R, and the target is last
	wantForwards := []string{"5555:127.0.0.1:5555", "2331:127.0.0.1:2331"}
	var forwards []string
	for i, a := range got {
		if a == "-R" && i+1 < len(got) {
			forwards = append(forwards, got[i+1])
		}
	}
	if !reflect.DeepEqual(forwards, wantForwards) {
		t.Errorf("forwards = %#v, want %#v", forwards, wantForwards)
	}
	if got[len(got)-1] != "user@host" {
		t.Errorf("target should be last arg, got %#v", got)
	}
}

func TestForwardFailedMatchesSSHsMessage(t *testing.T) {
	// The exact line OpenSSH prints when the remote bind is refused.
	if !ForwardFailed("Error: remote port forwarding failed for listen port 5556") {
		t.Error("should match ssh's forward-failure line")
	}
	if ForwardFailed("Permission denied (publickey,password).") {
		t.Error("an auth failure is not a forward failure")
	}
}

func TestEvictArgsTargetThenScript(t *testing.T) {
	got := EvictArgs("me@host", 5555, 5556)
	if got[len(got)-2] != "me@host" {
		t.Errorf("target should come before the remote command, got %#v", got)
	}
	if got[len(got)-1] != EvictScript(5555, 5556) {
		t.Errorf("last arg should be the eviction script")
	}
}

func TestEvictScriptOnlyKillsSSHDOnTheNamedPorts(t *testing.T) {
	s := EvictScript(5555, 2331)
	for _, want := range []string{"for p in 5555 2331;", `"sport = :$p"`, `= sshd ]`, "kill"} {
		if !strings.Contains(s, want) {
			t.Errorf("script missing %q:\n%s", want, s)
		}
	}
}

func TestBackoff(t *testing.T) {
	want := []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second}
	for i, w := range want {
		if got := Backoff(i); got != w {
			t.Errorf("Backoff(%d) = %v, want %v", i, got, w)
		}
	}
}
