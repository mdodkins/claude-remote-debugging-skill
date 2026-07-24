// Command serial-capture streams a board's serial console to stdout, an
// optional logfile, and (optionally) a TCP port — reconnecting automatically
// when the board resets and the USB serial re-enumerates. It is capture-only
// (it never writes to the port).
//
// With -tunnel it also brings up the reverse SSH tunnel itself, so a single
// command exposes the console on a remote host: it runs the system `ssh`
// (respecting your keys, ~/.ssh/config and password prompt) and warns clearly if
// the tunnel or the serial port has a problem.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"go.bug.st/serial"

	"serialcapture/internal/capture"
	"serialcapture/internal/tunnel"
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
	portPath := flag.String("port", "", "serial device path, e.g. /dev/ttyUSB0 (prefer a /dev/serial/by-id/... path — it survives re-enumeration)")
	baud := flag.Int("baud", 115200, "baud rate (C-Sound console is 115200 8N1)")
	logPath := flag.String("log", "", "also append captured lines to this file")
	tunnelTarget := flag.String("tunnel", "", "reverse-tunnel the stream to this SSH target, e.g. you@host.com — brings the tunnel up itself (prompts for your ssh password if you don't use keys)")
	tunnelPort := flag.Int("tunnel-port", 5555, "TCP port used on both ends of the tunnel")
	listen := flag.String("listen", "", "local-only: serve the stream on this TCP address, e.g. :5555 (ignored when -tunnel is set)")
	scrollback := flag.Int("scrollback", 200, "lines of recent history replayed to a newly-connected client")
	reconnect := flag.Duration("reconnect", time.Second, "delay between serial reconnect attempts")
	poll := flag.Duration("poll", 500*time.Millisecond, "how often to check for the serial device to reappear")
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
			log.Fatalf("serial-capture: cannot open log %q: %v", *logPath, err)
		}
		defer f.Close()
		sinks = append(sinks, func(l string) { fmt.Fprintln(f, l) })
	}

	// A tunnel implies serving on 127.0.0.1:<tunnel-port> (both ends pinned to
	// IPv4 to avoid localhost v4/v6 mismatches). Otherwise -listen is optional.
	serveAddr := *listen
	if *tunnelTarget != "" {
		serveAddr = fmt.Sprintf("127.0.0.1:%d", *tunnelPort)
	}

	if serveAddr != "" {
		bc := capture.NewBroadcaster(*scrollback)
		ln, err := net.Listen("tcp", serveAddr)
		if err != nil {
			log.Fatalf("serial-capture: cannot listen on %s: %v", serveAddr, err)
		}
		defer ln.Close()
		go acceptLoop(ctx, ln, bc)
		sinks = append(sinks, bc.Broadcast)
		log.Printf("serial-capture: serving stream on %s (scrollback %d lines)", serveAddr, *scrollback)
	}

	if *tunnelTarget != "" {
		startTunnel(ctx, *tunnelTarget, *tunnelPort)
	}

	emit := func(l string) {
		for _, s := range sinks {
			s(l)
		}
	}

	if _, err := os.Stat(*portPath); err != nil {
		log.Printf("serial-capture: %s not present yet — waiting for it to appear", *portPath)
	}
	log.Printf("serial-capture: capturing %s @ %d 8N1 (auto-reconnect on reset)", *portPath, *baud)
	o := &serialOpener{path: *portPath, baud: *baud, poll: *poll}
	capture.Run(ctx, o, time.Now, emit, *reconnect)
}

// startTunnel launches the reverse SSH tunnel as a child process, inheriting the
// terminal so ssh's own password prompt works, and warns loudly if it exits.
func startTunnel(ctx context.Context, target string, port int) {
	if _, err := exec.LookPath("ssh"); err != nil {
		log.Fatalf("serial-capture: -tunnel needs the `ssh` client on your PATH: %v", err)
	}
	args := tunnel.SSHArgs(target, port, port)
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Stdin = os.Stdin   // let ssh prompt for a password if needed
	cmd.Stdout = os.Stderr // keep our stdout (the captured stream) clean
	cmd.Stderr = os.Stderr

	log.Printf("serial-capture: opening reverse tunnel — ssh %s", strings.Join(args, " "))
	log.Printf("serial-capture: (enter your ssh password if prompted; remote clients read localhost:%d on %s)", port, target)
	if err := cmd.Start(); err != nil {
		log.Fatalf("serial-capture: failed to start ssh: %v", err)
	}

	go func() {
		err := cmd.Wait()
		if ctx.Err() != nil {
			return // our own shutdown killed it
		}
		log.Printf("serial-capture: WARNING — the ssh tunnel exited (%v). Capture continues locally, but the remote end is no longer receiving data. Common causes: wrong host or credentials, host unreachable, or remote port %d already in use on the server.", err, port)
	}()
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
