package capture

import (
	"io"
	"sync"
)

// Broadcaster fans out captured lines to a dynamic set of writer clients (TCP
// connections in practice). A client whose write fails is dropped. It keeps a
// bounded scrollback of the most recent lines and replays it to each newly
// added client, so connecting mid-session shows recent context instead of a
// blank screen. It is safe for concurrent use.
type Broadcaster struct {
	mu         sync.Mutex
	clients    map[io.Writer]struct{}
	history    []string
	maxHistory int
}

// NewBroadcaster returns an empty Broadcaster that replays up to historySize
// recent lines to each new client (0 disables scrollback).
func NewBroadcaster(historySize int) *Broadcaster {
	return &Broadcaster{clients: map[io.Writer]struct{}{}, maxHistory: historySize}
}

// Add registers a client to receive subsequent broadcast lines, first replaying
// the current scrollback to it. A client whose write fails during replay is not
// registered.
func (b *Broadcaster) Add(w io.Writer) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, line := range b.history {
		if _, err := w.Write([]byte(line + "\n")); err != nil {
			return // client died during replay; don't register it
		}
	}
	b.clients[w] = struct{}{}
}

// Broadcast writes line (with a trailing newline) to every client, dropping any
// client whose write fails.
func (b *Broadcaster) Broadcast(line string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.maxHistory > 0 {
		b.history = append(b.history, line)
		if len(b.history) > b.maxHistory {
			b.history = b.history[len(b.history)-b.maxHistory:]
		}
	}
	data := []byte(line + "\n")
	for w := range b.clients {
		if _, err := w.Write(data); err != nil {
			delete(b.clients, w)
		}
	}
}

// Count returns the number of connected clients.
func (b *Broadcaster) Count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.clients)
}
