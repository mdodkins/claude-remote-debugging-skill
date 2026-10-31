package main

import (
	"bufio"
	"context"
	"net"
	"testing"
	"time"

	"debugbridge/internal/capture"
	"debugbridge/internal/client"
)

// A serial-console client must be able to tell replayed scrollback from live
// output, or a waiter matches the previous run's "done" line.
func TestAcceptLoopSendsScrollbackThenBannerThenLiveLines(t *testing.T) {
	bc := capture.NewBroadcaster(10)
	bc.Broadcast("old run: done")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go acceptLoop(ctx, ln, bc)

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for deadline := time.Now().Add(2 * time.Second); bc.Count() == 0; {
		if time.Now().After(deadline) {
			t.Fatal("client never registered")
		}
		time.Sleep(5 * time.Millisecond)
	}
	bc.Broadcast("new run: started")

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	r := bufio.NewReader(conn)
	for _, want := range []string{"old run: done", client.Banner, "new run: started"} {
		got, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("reading %q: %v", want, err)
		}
		if got != want+"\n" {
			t.Fatalf("got %q, want %q", got, want+"\n")
		}
	}
}
