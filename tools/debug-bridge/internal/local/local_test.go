package local

import (
	"reflect"
	"testing"
	"time"
)

func TestNewWindowArgsInsideTmuxOpensAWindowInTheCurrentSession(t *testing.T) {
	got := NewWindowArgs(true, "debug-bridge", "agent", "/home/me/proj", "")
	want := []string{"new-window", "-n", "agent", "-c", "/home/me/proj", "-P", "-F", "#{pane_id}"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %#v\nwant %#v", got, want)
	}
}

func TestNewWindowArgsOutsideTmuxStartsADetachedSession(t *testing.T) {
	got := NewWindowArgs(false, "debug-bridge", "agent", "/home/me/proj", "")
	want := []string{"new-session", "-d", "-s", "debug-bridge", "-n", "agent", "-c", "/home/me/proj", "-P", "-F", "#{pane_id}"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %#v\nwant %#v", got, want)
	}
}

func TestNewWindowArgsOptionalShellIsTheWindowCommand(t *testing.T) {
	got := NewWindowArgs(true, "s", "agent", "/d", "/bin/zsh -l")
	if got[len(got)-1] != "/bin/zsh -l" {
		t.Errorf("shell should be the trailing window command, got %#v", got)
	}
}

func TestPipePaneArgsStreamPaneOutputToTheFifo(t *testing.T) {
	got := PipePaneArgs("%7", "/tmp/db.fifo")
	want := []string{"pipe-pane", "-t", "%7", "cat > '/tmp/db.fifo'"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %#v\nwant %#v", got, want)
	}
	if !reflect.DeepEqual(StopPipeArgs("%7"), []string{"pipe-pane", "-t", "%7"}) {
		t.Errorf("stop should be pipe-pane with no command")
	}
}

func TestKillArgsRemoveOnlyWhatWeCreated(t *testing.T) {
	if got := KillArgs(true, "debug-bridge", "%7"); !reflect.DeepEqual(got, []string{"kill-window", "-t", "%7"}) {
		t.Errorf("inside tmux: kill just our window, got %#v", got)
	}
	if got := KillArgs(false, "debug-bridge", "%7"); !reflect.DeepEqual(got, []string{"kill-session", "-t", "debug-bridge"}) {
		t.Errorf("outside tmux: kill the session we made, got %#v", got)
	}
}

func TestSendLineArgsTypesTheLineLiterallyThenEnter(t *testing.T) {
	got := SendLineArgs("%7", "make -j4 && echo done; ls -l")
	want := [][]string{
		{"send-keys", "-t", "%7", "-l", "--", "make -j4 && echo done; ls -l"},
		{"send-keys", "-t", "%7", "Enter"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %#v\nwant %#v", got, want)
	}
}

func TestSendLineArgsControlTokens(t *testing.T) {
	cases := map[string]string{"^C": "C-c", "^D": "C-d", "^Z": "C-z", "^L": "C-l", "^\\": "C-\\"}
	for tok, key := range cases {
		got := SendLineArgs("%1", tok)
		want := [][]string{{"send-keys", "-t", "%1", key}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %#v want %#v", tok, got, want)
		}
	}
	// an empty line is just Enter (e.g. to answer a "press enter" prompt)
	if got := SendLineArgs("%1", ""); !reflect.DeepEqual(got, [][]string{{"send-keys", "-t", "%1", "Enter"}}) {
		t.Errorf("empty line: got %#v", got)
	}
	// a line that merely starts with ^ is literal text, not a control key
	if got := SendLineArgs("%1", "^Cnot"); len(got) != 2 || got[0][4] != "--" {
		t.Errorf("'^Cnot' must be typed literally, got %#v", got)
	}
}

func TestCleanStripsAnsiAndKeepsTextAfterTheLastCarriageReturn(t *testing.T) {
	cases := []struct{ in, want string }{
		{"\x1b[32mok\x1b[0m built", "ok built"},
		{"\x1b]0;title\x07prompt$ ", "prompt$ "},                       // OSC title sequence
		{"progress 10%\rprogress 50%\rprogress 100%", "progress 100%"}, // CR overwrites
		{"plain", "plain"},
		{"\x1b[?2004hme@host:~$ ", "me@host:~$ "}, // bracketed-paste mode on
		{"tab\there", "tab\there"},                // other control chars kept
	}
	for _, c := range cases {
		if got := Clean(c.in); got != c.want {
			t.Errorf("Clean(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSplitterEmitsCompleteLinesAndFlushesThePartialOnIdle(t *testing.T) {
	s := NewSplitter()
	lines := s.Feed([]byte("one\ntwo\npro"))
	if !reflect.DeepEqual(lines, []string{"one", "two"}) {
		t.Fatalf("complete lines: got %#v", lines)
	}
	// nothing more arrives: a prompt with no newline must still come out
	if got, ok := s.Flush(); !ok || got != "pro" {
		t.Fatalf("flush: got %q ok=%v", got, ok)
	}
	if _, ok := s.Flush(); ok {
		t.Fatalf("second flush must be empty")
	}
	// data after a flush starts a fresh line
	if lines := s.Feed([]byte("mpt$ ls\nout\n")); !reflect.DeepEqual(lines, []string{"mpt$ ls", "out"}) {
		t.Fatalf("after flush: got %#v", lines)
	}
}

func TestSplitterCRLFDoesNotLeaveAStrayCR(t *testing.T) {
	s := NewSplitter()
	if lines := s.Feed([]byte("a\r\nb\r\n")); !reflect.DeepEqual(lines, []string{"a", "b"}) {
		t.Fatalf("got %#v", lines)
	}
}

func TestIdleFlushDefaultIsShortEnoughForPrompts(t *testing.T) {
	if IdleFlush > 500*time.Millisecond || IdleFlush < 50*time.Millisecond {
		t.Errorf("IdleFlush %v: a prompt should appear promptly but not split normal output", IdleFlush)
	}
}
