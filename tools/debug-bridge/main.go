// Command debug-bridge streams a board's serial console to stdout, an
// optional logfile, and (optionally) a TCP port — reconnecting automatically
// when the board resets and the USB serial re-enumerates. It is capture-only
// (it never writes to the port).
//
// With -tunnel it also brings up the reverse SSH tunnel itself, so a single
// command exposes the console on a remote host: it runs the system `ssh`
// (respecting your keys, ~/.ssh/config and password prompt) and warns clearly if
// the tunnel or the serial port has a problem.
//
// With -local it instead (or additionally) opens a tmux window on this machine
// and serves it on a TCP port: lines received are typed into the window and
// the window's output streams back, so a remote agent can drive a shell, gdb or
// any REPL here while you watch. -connect is the matching client.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"go.bug.st/serial"

	"debugbridge/internal/capture"
	"debugbridge/internal/client"
	"debugbridge/internal/local"
	"debugbridge/internal/tunnel"
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
	noEvict := flag.Bool("no-evict", false, "with -tunnel: if the server refuses a forwarded port, do not try to end your own stale session holding it; just retry")
	tunnelPort := flag.Int("tunnel-port", 5555, "TCP port used on both ends of the serial tunnel")
	gdbPort := flag.Int("gdb-port", 0, "also forward this port through the tunnel, e.g. 2331 for the J-Link GDB server (0 = serial only)")
	listen := flag.String("listen", "", "local-only: serve the stream on this TCP address, e.g. :5555 (ignored when -tunnel is set)")
	scrollback := flag.Int("scrollback", 200, "lines of recent history replayed to a newly-connected client")
	reconnect := flag.Duration("reconnect", time.Second, "delay between serial reconnect attempts")
	poll := flag.Duration("poll", 500*time.Millisecond, "how often to check for the serial device to reappear")

	localMode := flag.Bool("local", false, "open a tmux window on this machine that the agent can type into and read from (serves it on -local-port, and through -tunnel if set)")
	localPort := flag.Int("local-port", 5556, "TCP port for the -local window, on both ends of the tunnel")
	localName := flag.String("local-name", "agent", "tmux window name for -local")
	localSession := flag.String("local-session", "debug-bridge", "tmux session created for -local when not already inside tmux")
	localDir := flag.String("local-dir", "", "directory the -local window's shell starts in (default: current directory)")
	localShell := flag.String("local-shell", "", "command run in the -local window (default: tmux's default-shell)")
	attach := flag.Bool("attach", false, "with -local outside tmux: attach this terminal to the window once the tunnel is up (detach with prefix-d; the bridge keeps running)")

	connect := flag.String("connect", "", "client mode: connect to a -local window at this address, e.g. 127.0.0.1:5556")
	cmd := flag.String("cmd", "", "client mode: line to type into the window (omit to just stream its output)")
	until := flag.String("until", "", "client mode: stop when an output line matches this regexp, e.g. the prompt '\\$ $'")
	quiet := flag.Duration("quiet", 2*time.Second, "client mode: stop when the window has printed nothing for this long (0 = never)")
	timeout := flag.Duration("timeout", 60*time.Second, "client mode: give up this long after sending the command (exit status 124)")
	flag.Parse()

	if *connect != "" {
		os.Exit(runClient(*connect, *cmd, *until, *quiet, *timeout))
	}

	if *portPath == "" && !*localMode {
		fmt.Fprintln(os.Stderr, "debug-bridge: -port (serial console) and/or -local (tmux window) is required")
		flag.Usage()
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	sinks := []func(string){func(l string) { fmt.Println(l) }}

	if *logPath != "" {
		f, err := os.OpenFile(*logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			log.Fatalf("debug-bridge: cannot open log %q: %v", *logPath, err)
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

	if *portPath == "" {
		serveAddr = "" // no serial stream to serve
	}
	if serveAddr != "" {
		bc := capture.NewBroadcaster(*scrollback)
		ln, err := net.Listen("tcp", serveAddr)
		if err != nil {
			log.Fatalf("debug-bridge: cannot listen on %s: %v", serveAddr, err)
		}
		defer ln.Close()
		go acceptLoop(ctx, ln, bc)
		sinks = append(sinks, bc.Broadcast)
		log.Printf("debug-bridge: serving stream on %s (scrollback %d lines)", serveAddr, *scrollback)
	}

	var window *local.Window
	if *localMode {
		dir := *localDir
		if dir == "" {
			dir, _ = os.Getwd()
		}
		w, err := local.Open(local.Options{Session: *localSession, Name: *localName, Dir: dir, Shell: *localShell})
		if err != nil {
			log.Fatalf("debug-bridge: %v", err)
		}
		window = w
		defer window.Close()
		addr := fmt.Sprintf("127.0.0.1:%d", *localPort)
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			log.Fatalf("debug-bridge: cannot listen on %s: %v", addr, err)
		}
		defer ln.Close()
		wbc := capture.NewBroadcaster(*scrollback)
		go localAcceptLoop(ctx, ln, wbc, window)
		go func() {
			if err := window.Run(ctx, wbc.Broadcast); err != nil && ctx.Err() == nil {
				log.Printf("debug-bridge: WARNING — the local window's output stream ended (%v); was the window closed?", err)
			}
		}()
		log.Printf("debug-bridge: local window %s (pane %s, shell in %s) served on %s — %s", *localName, window.Pane, dir, addr, window.AttachHint())
	}

	if *tunnelTarget != "" {
		var ports []int
		if *portPath != "" {
			ports = append(ports, *tunnelPort)
		}
		if *gdbPort > 0 {
			ports = append(ports, *gdbPort)
		}
		if *localMode {
			ports = append(ports, *localPort)
		}
		startTunnel(ctx, *tunnelTarget, ports, !*noEvict)
	}

	if *localMode && *attach && !local.InsideTmux() {
		// Give ssh its moment with the terminal (password prompt) before tmux takes it.
		if *tunnelTarget != "" {
			time.Sleep(3 * time.Second)
		}
		go func() {
			_ = window.Attach(ctx)
			if ctx.Err() == nil {
				log.Printf("debug-bridge: detached from the window; it is still being served — %s", window.AttachHint())
			}
		}()
	}

	if *portPath == "" {
		<-ctx.Done() // -local only: run until interrupted
		return
	}

	emit := func(l string) {
		for _, s := range sinks {
			s(l)
		}
	}

	if _, err := os.Stat(*portPath); err != nil {
		log.Printf("debug-bridge: %s not present yet — waiting for it to appear", *portPath)
	}
	log.Printf("debug-bridge: capturing %s @ %d 8N1 (auto-reconnect on reset)", *portPath, *baud)
	o := &serialOpener{path: *portPath, baud: *baud, poll: *poll}
	capture.Run(ctx, o, time.Now, emit, *reconnect)
}

// startTunnel keeps the reverse SSH tunnel up for as long as the bridge runs,
// forwarding each of `ports` (the serial stream, the J-Link GDB port, the
// -local window). ssh inherits the terminal so its password prompt works.
//
// When ssh exits it is started again, with backoff. If it exited because the
// server refused a forward, the port is almost always still held by this
// user's own previous session, whose link died without a clean close (the
// laptop moved from one uplink to another): sshd keeps that dead session, and
// its listening port, until its own keepalive gives up, which by default is
// hours. So before retrying, the bridge asks the server to end that stale
// session (tunnel.EvictArgs: same user, sshd only, those ports only).
// -no-evict turns that off.
func startTunnel(ctx context.Context, target string, ports []int, evict bool) {
	if _, err := exec.LookPath("ssh"); err != nil {
		log.Fatalf("debug-bridge: -tunnel needs the `ssh` client on your PATH: %v", err)
	}
	go func() {
		for attempt := 0; ; attempt++ {
			start := time.Now()
			refused := runTunnel(ctx, target, ports)
			if ctx.Err() != nil {
				return // our own shutdown killed it
			}
			if time.Since(start) > time.Minute {
				attempt = 0 // it was up for a while: this is a fresh drop, not a retry storm
			}
			if refused && evict {
				log.Printf("debug-bridge: the server refused port(s) %v, most likely held by your previous session that dropped; asking it to let go", ports)
				evictStale(ctx, target, ports)
			}
			wait := tunnel.Backoff(attempt)
			log.Printf("debug-bridge: WARNING — the ssh tunnel is down; capture continues locally. Reconnecting in %v.", wait)
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
		}
	}()
}

// runTunnel runs one ssh session until it exits, passing ssh's stderr through
// to ours, and reports whether it died because a remote forward was refused.
func runTunnel(ctx context.Context, target string, ports []int) (refused bool) {
	args := tunnel.SSHArgs(target, ports...)
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Stdin = os.Stdin   // let ssh prompt for a password if needed
	cmd.Stdout = os.Stderr // keep our stdout (the captured stream) clean
	stderr, err := cmd.StderrPipe()
	if err != nil {
		log.Printf("debug-bridge: cannot read ssh's errors: %v", err)
		return false
	}
	log.Printf("debug-bridge: opening reverse tunnel — ssh %s", strings.Join(args, " "))
	log.Printf("debug-bridge: (enter your ssh password if prompted; forwarding ports %v to %s)", ports, target)
	if err := cmd.Start(); err != nil {
		log.Printf("debug-bridge: failed to start ssh: %v", err)
		return false
	}
	sc := bufio.NewScanner(stderr)
	for sc.Scan() {
		line := sc.Text()
		fmt.Fprintln(os.Stderr, line)
		if tunnel.ForwardFailed(line) {
			refused = true
		}
	}
	err = cmd.Wait()
	if ctx.Err() == nil {
		log.Printf("debug-bridge: the ssh tunnel exited (%v).", err)
	}
	return refused
}

// evictStale runs tunnel.EvictArgs once and logs what the server freed.
func evictStale(ctx context.Context, target string, ports []int) {
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, "ssh", tunnel.EvictArgs(target, ports...)...)
	cmd.Stdin = os.Stdin
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l != "" {
			log.Printf("debug-bridge: server: %s", l)
		}
	}
	if err != nil {
		log.Printf("debug-bridge: could not clear the stale session (%v); will keep retrying until the server drops it", err)
	} else if len(strings.TrimSpace(string(out))) == 0 {
		log.Printf("debug-bridge: nothing of yours was holding %v; something else has the port, or it was already freed", ports)
	}
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

// localAcceptLoop serves the -local window: each client gets the scrollback,
// the live banner, then the window's output; every line it sends is typed
// into the window. Both directions are logged so the human sees what the
// agent is doing even without watching tmux.
func localAcceptLoop(ctx context.Context, ln net.Listener, bc *capture.Broadcaster, w *local.Window) {
	go func() { <-ctx.Done(); ln.Close() }()
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		bc.AddBanner(&deadlineWriter{conn: conn}, client.Banner)
		go func(conn net.Conn) {
			sc := bufio.NewScanner(conn)
			sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
			for sc.Scan() {
				line := strings.TrimRight(sc.Text(), "\r")
				log.Printf("debug-bridge: agent> %s", line)
				if err := w.SendLine(line); err != nil {
					log.Printf("debug-bridge: WARNING — could not type into the window: %v", err)
					bc.Broadcast("[debug-bridge] send failed: " + err.Error())
				}
			}
		}(conn)
	}
}

// runClient implements -connect: one command in, its output out. Exit status
// 0 when the prompt pattern matched or the window went quiet, 124 on timeout.
func runClient(addr, cmd, until string, quiet, timeout time.Duration) int {
	var re *regexp.Regexp
	if until != "" {
		var err error
		if re, err = regexp.Compile(until); err != nil {
			fmt.Fprintf(os.Stderr, "debug-bridge: bad -until regexp: %v\n", err)
			return 2
		}
	}
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "debug-bridge: cannot connect to %s (is the bridge running with -local, and the tunnel up?): %v\n", addr, err)
		return 1
	}
	defer conn.Close()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	res, err := client.Run(ctx, conn, os.Stdout, client.Options{Cmd: cmd, Until: re, Quiet: quiet, Timeout: timeout})
	if err != nil && ctx.Err() == nil {
		fmt.Fprintf(os.Stderr, "debug-bridge: %v\n", err)
		return 1
	}
	if res.TimedOut {
		fmt.Fprintf(os.Stderr, "debug-bridge: no prompt/quiet within %v — the command may still be running (connect again to keep reading, or send ^C)\n", timeout)
		return 124
	}
	return 0
}
