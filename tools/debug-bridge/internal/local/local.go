// Package local turns a tmux window into a channel an agent can drive: lines
// sent to it are typed into the window, and the window's output streams back.
// It wraps the system `tmux` (new-window / pipe-pane / send-keys) rather than
// emulating a terminal, so whatever runs in the window — a shell, gdb, a REPL —
// is visible to the human in tmux exactly as the agent sees it.
//
// This file holds the pure parts (command builders and output cleaning) so
// they can be unit-tested without tmux; window.go wires them to real processes.
package local

import (
	"regexp"
	"strings"
	"time"
)

// IdleFlush is how long a partial line (no trailing newline yet) may sit before
// it is emitted anyway. Prompts never end in a newline, so without this the
// agent would never see "user@host:~$ " and could not tell a command finished.
const IdleFlush = 200 * time.Millisecond

// NewWindowArgs builds the tmux command that opens the agent window with its
// shell started in dir. Inside tmux (TMUX set) it is a new window in the user's
// current session, which pops up in front of them; outside it is a new detached
// session they can attach to. -P -F prints the pane id so we can target it.
// An empty shell leaves tmux's default-shell in charge.
func NewWindowArgs(insideTmux bool, session, name, dir, shell string) []string {
	var args []string
	if insideTmux {
		args = []string{"new-window", "-n", name}
	} else {
		args = []string{"new-session", "-d", "-s", session, "-n", name}
	}
	args = append(args, "-c", dir, "-P", "-F", "#{pane_id}")
	if shell != "" {
		args = append(args, shell)
	}
	return args
}

// PipePaneArgs makes tmux copy everything the pane prints into fifo (a named
// pipe we read). Without -o the pipe is set unconditionally, replacing any
// previous one.
func PipePaneArgs(pane, fifo string) []string {
	return []string{"pipe-pane", "-t", pane, "cat > '" + fifo + "'"}
}

// StopPipeArgs closes the pane's output pipe (pipe-pane with no command).
func StopPipeArgs(pane string) []string {
	return []string{"pipe-pane", "-t", pane}
}

// KillArgs removes what NewWindowArgs created: only our window when we joined
// the user's session, the whole session when we made one.
func KillArgs(insideTmux bool, session, pane string) []string {
	if insideTmux {
		return []string{"kill-window", "-t", pane}
	}
	return []string{"kill-session", "-t", session}
}

// controlTokens are the lines an agent sends to press a control key instead of
// typing text: "^C" interrupts the running program, "^D" sends EOF, and so on.
var controlTokens = map[string]string{
	"^C": "C-c", "^D": "C-d", "^Z": "C-z", "^L": "C-l", "^\\": "C-\\",
}

// SendLineArgs builds the tmux command(s) that deliver one received line to the
// pane: a control token presses that key; an empty line is Enter; anything else
// is typed literally (-l, so "$HOME" or ";" are not interpreted by tmux) and
// followed by Enter.
func SendLineArgs(pane, line string) [][]string {
	if key, ok := controlTokens[line]; ok {
		return [][]string{{"send-keys", "-t", pane, key}}
	}
	if line == "" {
		return [][]string{{"send-keys", "-t", pane, "Enter"}}
	}
	return [][]string{
		{"send-keys", "-t", pane, "-l", "--", line},
		{"send-keys", "-t", pane, "Enter"},
	}
}

// ansi matches CSI sequences (colours, cursor moves, bracketed-paste toggles)
// and OSC sequences (window titles) terminated by BEL or ST.
var ansi = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)`)

// Clean makes a raw pane line readable: escape sequences are removed and, as a
// terminal would show it, only the text after the last carriage return is kept
// (progress bars redraw with CR; the final state is what matters).
func Clean(line string) string {
	line = ansi.ReplaceAllString(line, "")
	if i := strings.LastIndexByte(line, '\r'); i >= 0 {
		line = line[i+1:]
	}
	return line
}

// Splitter cuts a byte stream into lines, holding back an incomplete final line
// until more data arrives or the caller flushes it (see IdleFlush).
type Splitter struct {
	partial []byte
}

// NewSplitter returns an empty Splitter.
func NewSplitter() *Splitter { return &Splitter{} }

// Feed appends data and returns every complete line in it (without the newline
// or a preceding carriage return). Anything after the last newline is held.
func (s *Splitter) Feed(data []byte) []string {
	s.partial = append(s.partial, data...)
	var lines []string
	for {
		i := strings.IndexByte(string(s.partial), '\n')
		if i < 0 {
			break
		}
		lines = append(lines, strings.TrimSuffix(string(s.partial[:i]), "\r"))
		s.partial = s.partial[i+1:]
	}
	return lines
}

// Flush returns the held partial line, if any, and clears it.
func (s *Splitter) Flush() (string, bool) {
	if len(s.partial) == 0 {
		return "", false
	}
	line := string(s.partial)
	s.partial = s.partial[:0]
	return line, true
}
