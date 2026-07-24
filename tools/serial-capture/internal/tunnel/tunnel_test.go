package tunnel

import (
	"reflect"
	"testing"
)

func TestSSHArgsSamePortBothEnds(t *testing.T) {
	got := SSHArgs("me@shard.example.com", 5555, 5555)
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

func TestSSHArgsDistinctRemotePort(t *testing.T) {
	got := SSHArgs("user@host", 6000, 5555)
	// remote:127.0.0.1:local
	want := "6000:127.0.0.1:5555"
	found := false
	for i, a := range got {
		if a == "-R" && i+1 < len(got) && got[i+1] == want {
			found = true
		}
	}
	if !found {
		t.Errorf("expected -R %q in %#v", want, got)
	}
	if got[len(got)-1] != "user@host" {
		t.Errorf("target should be last arg, got %#v", got)
	}
}
