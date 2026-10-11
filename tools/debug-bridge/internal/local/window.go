//go:build linux || darwin || freebsd || openbsd || netbsd

package local

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Window is a live tmux window the agent can type into and read from.
type Window struct {
	Pane       string // tmux pane id, e.g. "%7"
	Session    string
	insideTmux bool
	fifo       string
	dir        string
}

// Options for Open.
type Options struct {
	Session string // tmux session name used when we are not already inside tmux
	Name    string // window name
	Dir     string // directory the window's shell starts in
	Shell   string // window command; "" = tmux's default-shell
}

// InsideTmux reports whether this process runs inside a tmux client.
func InsideTmux() bool { return os.Getenv("TMUX") != "" }

// Open creates the window and starts piping its output into a FIFO. Call Run to
// stream that output, SendLine to type into it, and Close to tear it down.
func Open(opts Options) (*Window, error) {
	if _, err := exec.LookPath("tmux"); err != nil {
		return nil, fmt.Errorf("-local needs tmux on your PATH: %w", err)
	}
	inside := InsideTmux()
	out, err := exec.Command("tmux", NewWindowArgs(inside, opts.Session, opts.Name, opts.Dir, opts.Shell)...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("tmux could not open the window: %v: %s", err, strings.TrimSpace(string(out)))
	}
	w := &Window{Pane: strings.TrimSpace(string(out)), Session: opts.Session, insideTmux: inside}

	dir, err := os.MkdirTemp("", "debug-bridge-")
	if err != nil {
		w.kill()
		return nil, err
	}
	w.dir = dir
	w.fifo = filepath.Join(dir, "pane.fifo")
	if err := syscall.Mkfifo(w.fifo, 0o600); err != nil {
		w.kill()
		os.RemoveAll(dir)
		return nil, fmt.Errorf("cannot create fifo: %w", err)
	}
	if out, err := exec.Command("tmux", PipePaneArgs(w.Pane, w.fifo)...).CombinedOutput(); err != nil {
		w.Close()
		return nil, fmt.Errorf("tmux pipe-pane failed: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return w, nil
}

// Run reads the pane's output until ctx is done, calling emit once per cleaned
// line. A partial line (a prompt) is emitted after IdleFlush of silence.
func (w *Window) Run(ctx context.Context, emit func(string)) error {
	// Opening a FIFO for reading blocks until the writer (tmux's `cat`) opens
	// it; do it in a goroutine so ctx can still cancel us.
	type opened struct {
		f   *os.File
		err error
	}
	ch := make(chan opened, 1)
	go func() {
		f, err := os.OpenFile(w.fifo, os.O_RDONLY, 0)
		ch <- opened{f, err}
	}()
	var f *os.File
	select {
	case <-ctx.Done():
		return ctx.Err()
	case o := <-ch:
		if o.err != nil {
			return o.err
		}
		f = o.f
	}
	defer f.Close()
	go func() { <-ctx.Done(); f.Close() }()

	data := make(chan []byte)
	errs := make(chan error, 1)
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := f.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				data <- chunk
			}
			if err != nil {
				errs <- err
				return
			}
		}
	}()

	sp := NewSplitter()
	idle := time.NewTimer(IdleFlush)
	idle.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errs:
			if line, ok := sp.Flush(); ok {
				emit(Clean(line))
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		case chunk := <-data:
			for _, line := range sp.Feed(chunk) {
				emit(Clean(line))
			}
			idle.Stop()
			idle.Reset(IdleFlush)
		case <-idle.C:
			if line, ok := sp.Flush(); ok {
				emit(Clean(line))
			}
		}
	}
}

// SendLine types one received line into the window (see SendLineArgs).
func (w *Window) SendLine(line string) error {
	for _, args := range SendLineArgs(w.Pane, line) {
		if out, err := exec.Command("tmux", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("tmux send-keys: %v: %s", err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// Close stops the output pipe, removes the window (or session) we created and
// the FIFO.
func (w *Window) Close() {
	_ = exec.Command("tmux", StopPipeArgs(w.Pane)...).Run()
	w.kill()
	if w.dir != "" {
		os.RemoveAll(w.dir)
	}
}

func (w *Window) kill() {
	_ = exec.Command("tmux", KillArgs(w.insideTmux, w.Session, w.Pane)...).Run()
}

// AttachHint tells the user how to see the window when it is not already in
// front of them.
func (w *Window) AttachHint() string {
	if w.insideTmux {
		return "the agent window is open in your current tmux session"
	}
	return "watch it with: tmux attach -t " + w.Session
}

// Attach runs `tmux attach` on the user's terminal until they detach (prefix d)
// or ctx ends. Only meaningful when we created the session ourselves.
func (w *Window) Attach(ctx context.Context) error {
	if w.insideTmux {
		return nil
	}
	cmd := exec.CommandContext(ctx, "tmux", "attach", "-t", w.Session)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}
