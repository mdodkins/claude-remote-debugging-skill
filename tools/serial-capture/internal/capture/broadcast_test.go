package capture

import (
	"errors"
	"strings"
	"testing"
)

// errWriter fails every write, to exercise client dropping.
type errWriter struct{}

func (errWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

func TestBroadcastWritesLineWithNewlineToClient(t *testing.T) {
	b := NewBroadcaster()
	var sink strings.Builder
	b.Add(&sink)

	b.Broadcast("hello")

	if got := sink.String(); got != "hello\n" {
		t.Errorf("got %q, want %q", got, "hello\n")
	}
}

func TestBroadcastWritesToAllClients(t *testing.T) {
	b := NewBroadcaster()
	var a, c strings.Builder
	b.Add(&a)
	b.Add(&c)

	b.Broadcast("x")

	if a.String() != "x\n" || c.String() != "x\n" {
		t.Errorf("got a=%q c=%q, want both %q", a.String(), c.String(), "x\n")
	}
}

func TestBroadcastDropsClientWhoseWriteFails(t *testing.T) {
	b := NewBroadcaster()
	b.Add(errWriter{})
	var good strings.Builder
	b.Add(&good)

	b.Broadcast("first")
	if b.Count() != 1 {
		t.Errorf("failing client should be dropped, Count=%d want 1", b.Count())
	}

	b.Broadcast("second")
	if good.String() != "first\nsecond\n" {
		t.Errorf("good client got %q", good.String())
	}
}
