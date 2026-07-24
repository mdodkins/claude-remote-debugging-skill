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
	b := NewBroadcaster(0)
	var sink strings.Builder
	b.Add(&sink)

	b.Broadcast("hello")

	if got := sink.String(); got != "hello\n" {
		t.Errorf("got %q, want %q", got, "hello\n")
	}
}

func TestBroadcastWritesToAllClients(t *testing.T) {
	b := NewBroadcaster(0)
	var a, c strings.Builder
	b.Add(&a)
	b.Add(&c)

	b.Broadcast("x")

	if a.String() != "x\n" || c.String() != "x\n" {
		t.Errorf("got a=%q c=%q, want both %q", a.String(), c.String(), "x\n")
	}
}

func TestNewClientReceivesRecentScrollback(t *testing.T) {
	b := NewBroadcaster(3) // keep last 3 lines
	b.Broadcast("a")
	b.Broadcast("b")
	b.Broadcast("c")
	b.Broadcast("d") // "a" now evicted; history = b, c, d

	var late strings.Builder
	b.Add(&late) // connects late — should get the scrollback immediately
	if got := late.String(); got != "b\nc\nd\n" {
		t.Errorf("scrollback got %q, want %q", got, "b\nc\nd\n")
	}

	b.Broadcast("e") // then live lines continue
	if got := late.String(); got != "b\nc\nd\ne\n" {
		t.Errorf("after live line got %q, want %q", got, "b\nc\nd\ne\n")
	}
}

func TestZeroScrollbackReplaysNothing(t *testing.T) {
	b := NewBroadcaster(0)
	b.Broadcast("old")
	var late strings.Builder
	b.Add(&late)
	if got := late.String(); got != "" {
		t.Errorf("with no scrollback a new client should get nothing, got %q", got)
	}
}

func TestBroadcastDropsClientWhoseWriteFails(t *testing.T) {
	b := NewBroadcaster(0)
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
