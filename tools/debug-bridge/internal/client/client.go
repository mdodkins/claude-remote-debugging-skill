// Package client is the agent side of -local: it connects to the bridge's
// window port, sends one command, and prints the window's output until a
// prompt pattern appears, the window goes quiet, or a timeout — giving an agent
// request/response semantics over what is really a live terminal.
package client

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"regexp"
	"time"
)

// Banner is the line the bridge writes to each new client after replaying
// scrollback and before any live output. A client sending a command discards
// everything up to it, so old output is never mistaken for the response.
const Banner = "[debug-bridge] --- live ---"

// Options control one Run.
type Options struct {
	Cmd     string         // line to type into the window; "" just streams the port
	Until   *regexp.Regexp // stop when an output line matches (e.g. the prompt)
	Quiet   time.Duration  // stop when no output arrives for this long (0 = never)
	Timeout time.Duration  // overall limit after the command is sent (0 = none)
}

// Result says why Run stopped.
type Result struct {
	Matched  bool // Until matched
	TimedOut bool // Timeout elapsed first
}

// Run drives conn per opts, copying output lines to out. Without a Cmd it
// streams until conn closes or ctx ends. With one, it waits for Banner, sends
// Cmd, then relays lines until Until matches, Quiet passes with nothing new, or
// Timeout; conn closing also ends it.
func Run(ctx context.Context, conn io.ReadWriter, out io.Writer, opts Options) (Result, error) {
	lines := make(chan string)
	readErr := make(chan error, 1)
	go func() {
		sc := bufio.NewScanner(conn)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			lines <- sc.Text()
		}
		readErr <- sc.Err()
	}()

	if opts.Cmd == "" {
		for {
			select {
			case <-ctx.Done():
				return Result{}, ctx.Err()
			case err := <-readErr:
				return Result{}, err
			case l := <-lines:
				fmt.Fprintln(out, l)
			}
		}
	}

	// Discard history up to the banner.
	for waiting := true; waiting; {
		select {
		case <-ctx.Done():
			return Result{}, ctx.Err()
		case err := <-readErr:
			if err == nil {
				err = io.ErrUnexpectedEOF
			}
			return Result{}, fmt.Errorf("connection ended before the bridge's live marker: %w", err)
		case l := <-lines:
			waiting = l != Banner
		}
	}

	if _, err := io.WriteString(conn, opts.Cmd+"\n"); err != nil {
		return Result{}, fmt.Errorf("sending command: %w", err)
	}

	var timeout <-chan time.Time
	if opts.Timeout > 0 {
		t := time.NewTimer(opts.Timeout)
		defer t.Stop()
		timeout = t.C
	}
	quiet := time.NewTimer(quietOrForever(opts.Quiet))
	defer quiet.Stop()

	for {
		select {
		case <-ctx.Done():
			return Result{}, ctx.Err()
		case err := <-readErr:
			return Result{}, err
		case <-timeout:
			return Result{TimedOut: true}, nil
		case <-quiet.C:
			return Result{}, nil
		case l := <-lines:
			fmt.Fprintln(out, l)
			if opts.Until != nil && opts.Until.MatchString(l) {
				return Result{Matched: true}, nil
			}
			quiet.Stop()
			quiet.Reset(quietOrForever(opts.Quiet))
		}
	}
}

func quietOrForever(d time.Duration) time.Duration {
	if d <= 0 {
		return 100 * 365 * 24 * time.Hour
	}
	return d
}
