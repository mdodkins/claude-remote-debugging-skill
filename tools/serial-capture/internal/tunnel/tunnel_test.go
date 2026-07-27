package tunnel

import (
	"reflect"
	"testing"
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
