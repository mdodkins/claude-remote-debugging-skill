// Package capture contains the serial-capture core: reading timestamped lines
// from a stream and running a reconnect loop that survives board resets. It is
// deliberately free of any serial/OS dependency so it can be unit-tested with
// plain readers; the real serial port is wired in by the caller.
package capture

import (
	"bufio"
	"context"
	"io"
	"strings"
	"time"
)

// TimeFormat is the timestamp prefix applied to every emitted line. Millisecond
// precision is enough to correlate serial output with GDB events by eye.
const TimeFormat = "2006-01-02 15:04:05.000"

// Clock returns the current time; injected so tests are deterministic.
type Clock func() time.Time

// Stream reads r line by line and calls emit once per line with the line text
// prefixed by a timestamp. A trailing carriage return (from CRLF output) is
// stripped. Any final line without a trailing newline is still emitted when r
// ends. Stream returns r's read error (nil on a clean EOF).
func Stream(r io.Reader, now Clock, emit func(string)) error {
	sc := bufio.NewScanner(r)
	// Allow long lines without failing (default limit is 64 KiB).
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		emit(now().Format(TimeFormat) + " " + line)
	}
	return sc.Err()
}

// Opener opens the underlying stream (the serial port). Open should block until
// the device is available or ctx is done, so a reset that removes the device
// path is handled by waiting for it to reappear.
type Opener interface {
	Open(ctx context.Context) (io.ReadCloser, error)
}

// Run streams timestamped lines from the port to emit, reconnecting whenever the
// stream ends — e.g. when the board resets and the USB serial re-enumerates.
// Each connect/disconnect transition is emitted as a "[serial-capture]" marker
// line so a reset shows up clearly in the log rather than as a silent gap. It
// retries a failed open after `retry`. Run returns when ctx is cancelled.
func Run(ctx context.Context, o Opener, now Clock, emit func(string), retry time.Duration) error {
	marker := func(msg string) {
		emit(now().Format(TimeFormat) + " [serial-capture] " + msg)
	}
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rc, err := o.Open(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			marker("open failed: " + err.Error() + "; retrying")
			if !sleep(ctx, retry) {
				return ctx.Err()
			}
			continue
		}
		marker("connected")
		streamErr := Stream(rc, now, emit)
		rc.Close()
		if streamErr != nil {
			marker("disconnected: " + streamErr.Error())
		} else {
			marker("disconnected (eof)")
		}
	}
}

// sleep waits for d or until ctx is done. Returns true if it slept the full
// duration, false if ctx was cancelled first. A non-positive d never blocks.
func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
