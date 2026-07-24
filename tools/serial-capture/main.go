// Command serial-capture streams a board's serial console to stdout, an
// optional logfile, and an optional TCP port — reconnecting automatically when
// the board resets and the USB serial re-enumerates. It is capture-only (it
// never writes to the port). Pair it with a reverse SSH tunnel to read a remote
// board's console from another machine (same idea as the GDB server port).
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.bug.st/serial"

	"serialcapture/internal/capture"
)

// serialOpener opens the serial port, first waiting for the device path to
// exist so a reset that removes the path is handled by waiting for it to return.
type serialOpener struct {
	path string
	baud int
	poll time.Duration
}

func (o *serialOpener) Open(ctx context.Context) (io.ReadCloser, error) {
	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if _, err := os.Stat(o.path); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(o.poll):
		}
	}
	port, err := serial.Open(o.path, &serial.Mode{BaudRate: o.baud})
	if err != nil {
		return nil, err
	}
	return port, nil
}

func main() {
	portPath := flag.String("port", "", "serial device path, e.g. /dev/ttyUSB0 (a /dev/serial/by-id/... path survives re-enumeration best)")
	baud := flag.Int("baud", 115200, "baud rate (C-Sound console is 115200 8N1)")
	logPath := flag.String("log", "", "also append captured lines to this file")
	listen := flag.String("listen", "", "also serve the live stream on this TCP address, e.g. :5555")
	reconnect := flag.Duration("reconnect", time.Second, "delay between reconnect attempts")
	poll := flag.Duration("poll", 500*time.Millisecond, "how often to check for the device to reappear")
	flag.Parse()

	if *portPath == "" {
		fmt.Fprintln(os.Stderr, "serial-capture: -port is required")
		flag.Usage()
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	sinks := []func(string){func(l string) { fmt.Println(l) }}

	if *logPath != "" {
		f, err := os.OpenFile(*logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			log.Fatalf("serial-capture: open log %q: %v", *logPath, err)
		}
		defer f.Close()
		sinks = append(sinks, func(l string) { fmt.Fprintln(f, l) })
	}

	if *listen != "" {
		bc := capture.NewBroadcaster()
		ln, err := net.Listen("tcp", *listen)
		if err != nil {
			log.Fatalf("serial-capture: listen %q: %v", *listen, err)
		}
		defer ln.Close()
		go acceptLoop(ctx, ln, bc)
		sinks = append(sinks, bc.Broadcast)
		log.Printf("serial-capture: serving live stream on %s", *listen)
	}

	emit := func(l string) {
		for _, s := range sinks {
			s(l)
		}
	}

	log.Printf("serial-capture: capturing %s @ %d 8N1 (auto-reconnect on reset)", *portPath, *baud)
	o := &serialOpener{path: *portPath, baud: *baud, poll: *poll}
	capture.Run(ctx, o, time.Now, emit, *reconnect)
}

// acceptLoop registers each incoming TCP client with the broadcaster.
func acceptLoop(ctx context.Context, ln net.Listener, bc *capture.Broadcaster) {
	go func() { <-ctx.Done(); ln.Close() }()
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		bc.Add(&deadlineWriter{conn: conn})
	}
}

// deadlineWriter bounds each write so one stalled TCP reader cannot block the
// whole capture; on failure it closes the connection (the broadcaster drops it).
type deadlineWriter struct{ conn net.Conn }

func (d *deadlineWriter) Write(p []byte) (int, error) {
	_ = d.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	n, err := d.conn.Write(p)
	if err != nil {
		d.conn.Close()
	}
	return n, err
}
