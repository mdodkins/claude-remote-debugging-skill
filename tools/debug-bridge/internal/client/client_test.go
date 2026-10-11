package client

import (
	"bufio"
	"context"
	"net"
	"regexp"
	"strings"
	"testing"
	"time"
)

// fakeServer plays the bridge: it replays scrollback, writes the banner, then
// echoes whatever command it receives followed by the given output lines.
func fakeServer(t *testing.T, scrollback, output []string, delay time.Duration) (net.Conn, <-chan string) {
	t.Helper()
	srv, cli := net.Pipe()
	received := make(chan string, 1)
	go func() {
		w := bufio.NewWriter(srv)
		for _, l := range scrollback {
			w.WriteString(l + "\n")
		}
		w.WriteString(Banner + "\n")
		w.Flush()
		cmd, _ := bufio.NewReader(srv).ReadString('\n')
		received <- strings.TrimSuffix(cmd, "\n")
		for _, l := range output {
			time.Sleep(delay)
			w.WriteString(l + "\n")
			w.Flush()
		}
		// keep the connection open like the real bridge would
		<-time.After(2 * time.Second)
		srv.Close()
	}()
	return cli, received
}

func TestRunSkipsScrollbackSendsTheCommandAndStopsOnTheUntilPattern(t *testing.T) {
	conn, received := fakeServer(t, []string{"old line", "older prompt$ "}, []string{"me@box:~$ make", "building", "done", "me@box:~$ "}, 5*time.Millisecond)
	var out strings.Builder
	res, err := Run(context.Background(), conn, &out, Options{
		Cmd: "make", Until: regexp.MustCompile(`\$ $`), Quiet: time.Second, Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := <-received; got != "make" {
		t.Errorf("server received %q", got)
	}
	if strings.Contains(out.String(), "old line") {
		t.Errorf("scrollback must be discarded, got %q", out.String())
	}
	want := "me@box:~$ make\nbuilding\ndone\nme@box:~$ \n"
	if out.String() != want {
		t.Errorf("got %q want %q", out.String(), want)
	}
	if !res.Matched || res.TimedOut {
		t.Errorf("result %+v, want Matched", res)
	}
}

func TestRunStopsAfterQuietPeriodWhenNoPatternGiven(t *testing.T) {
	conn, _ := fakeServer(t, nil, []string{"a", "b"}, 5*time.Millisecond)
	var out strings.Builder
	start := time.Now()
	res, err := Run(context.Background(), conn, &out, Options{Cmd: "ls", Quiet: 100 * time.Millisecond, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if out.String() != "a\nb\n" || res.Matched || res.TimedOut {
		t.Errorf("out %q res %+v", out.String(), res)
	}
	if el := time.Since(start); el > time.Second {
		t.Errorf("quiet exit took %v", el)
	}
}

func TestRunReportsTimeout(t *testing.T) {
	conn, _ := fakeServer(t, nil, []string{"still", "going"}, 60*time.Millisecond)
	var out strings.Builder
	res, err := Run(context.Background(), conn, &out, Options{Cmd: "sleep", Quiet: time.Second, Timeout: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut {
		t.Errorf("want TimedOut, got %+v (out %q)", res, out.String())
	}
}

func TestRunWithoutCommandStreamsEverythingIncludingScrollback(t *testing.T) {
	srv, cli := net.Pipe()
	go func() {
		srv.Write([]byte("old\n" + Banner + "\nnew\n"))
		srv.Close()
	}()
	var out strings.Builder
	if _, err := Run(context.Background(), cli, &out, Options{}); err != nil {
		t.Fatal(err)
	}
	if out.String() != "old\n"+Banner+"\nnew\n" {
		t.Errorf("got %q", out.String())
	}
}
