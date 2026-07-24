package capture

import (
	"io"
	"sync"
)

// Broadcaster fans out captured lines to a dynamic set of writer clients (TCP
// connections in practice). A client whose write fails is dropped. It is safe
// for concurrent use.
type Broadcaster struct {
	mu      sync.Mutex
	clients map[io.Writer]struct{}
}

// NewBroadcaster returns an empty Broadcaster.
func NewBroadcaster() *Broadcaster {
	return &Broadcaster{clients: map[io.Writer]struct{}{}}
}

// Add registers a client to receive subsequent broadcast lines.
func (b *Broadcaster) Add(w io.Writer) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.clients[w] = struct{}{}
}

// Broadcast writes line (with a trailing newline) to every client, dropping any
// client whose write fails.
func (b *Broadcaster) Broadcast(line string) {
	b.mu.Lock()
	defer b.mu.Unlock()
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
