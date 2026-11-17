//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

var bridgeBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "bridge-bin")
	if err != nil {
		panic(err)
	}
	bridgeBin = filepath.Join(dir, "debug-bridge")
	if out, err := exec.Command("go", "build", "-o", bridgeBin, ".").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build failed: %v\n%s", err, out)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// alive reports whether pid is a running process (a zombie is not).
func alive(pid int) bool {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	// "pid (comm) S ...": the state follows the last ')'.
	s := string(b)
	i := strings.LastIndex(s, ")")
	return i >= 0 && i+2 < len(s) && s[i+2] != 'Z'
}

// startBridgeWithFakeSSH runs the real bridge with -tunnel against a fake
// `ssh` that records its pid and then sits there like a tunnel would. It
// returns the bridge process and the fake ssh's pid once it is up.
func startBridgeWithFakeSSH(t *testing.T) (*exec.Cmd, int) {
	t.Helper()
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "ssh.pid")
	// Asked to stop (TERM), it lingers half a second, as a real ssh may while
	// it closes its channels; only KILL ends it at once.
	fake := "#!/bin/sh\ntrap 'sleep 0.5; exit 0' TERM\necho $$ > " + pidFile + "\nwhile :; do sleep 0.1; done\n"
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bridgeBin, "-port", filepath.Join(dir, "no-such-serial"), "-tunnel", "nobody@example.invalid")
	cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(pidFile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && alive(pid) {
				t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
				return cmd, pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the fake ssh never started: the bridge did not open its tunnel")
	return nil, 0
}

// stopBridge signals the bridge, waits for it to exit, and reports whether it
// was killed by the signal instead of shutting itself down.
func stopBridge(t *testing.T, bridge *exec.Cmd, sig syscall.Signal) (signaled bool) {
	t.Helper()
	if err := bridge.Process.Signal(sig); err != nil {
		t.Fatal(err)
	}
	st, _ := bridge.Process.Wait()
	ws, ok := st.Sys().(syscall.WaitStatus)
	return ok && ws.Signaled()
}

// A bridge asked to stop must not exit ahead of its own ssh: the moment the
// bridge is gone, the tunnel is gone too, and the bridge left by returning,
// not by dying of the signal.
func bridgeStopsItsTunnelFirst(t *testing.T, sig syscall.Signal) {
	bridge, sshPid := startBridgeWithFakeSSH(t)
	asked := time.Now()
	if stopBridge(t, bridge, sig) {
		t.Errorf("%v killed the bridge outright; it should shut itself down", sig)
	}
	if took := time.Since(asked); took > 2*time.Second {
		t.Errorf("the bridge took %v to stop after %v; it should go as soon as its ssh has", took.Round(time.Millisecond), sig)
	}
	if alive(sshPid) {
		t.Fatalf("after %v the bridge had exited but its ssh (pid %d) was still running: the server keeps the forwarded port", sig, sshPid)
	}
}

// A plain `kill` of the bridge must take the tunnel with it. Otherwise ssh
// lives on, the server's sshd keeps the port, and the next bridge cannot bind.
func TestTerminatedBridgeTakesItsTunnelWithIt(t *testing.T) {
	bridgeStopsItsTunnelFirst(t, syscall.SIGTERM)
}

// Closing the terminal the bridge runs in.
func TestHungUpBridgeTakesItsTunnelWithIt(t *testing.T) {
	bridgeStopsItsTunnelFirst(t, syscall.SIGHUP)
}

// kill -9 gives the bridge no chance to clean up; the kernel must do it.
func TestKilledBridgeTakesItsTunnelWithIt(t *testing.T) {
	bridge, sshPid := startBridgeWithFakeSSH(t)
	stopBridge(t, bridge, syscall.SIGKILL)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !alive(sshPid) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the bridge was killed but its ssh (pid %d) was still running 3s later: the server keeps the forwarded port", sshPid)
}

// The control for the three above: while the bridge runs, so does its tunnel.
func TestTunnelStaysUpWhileTheBridgeRuns(t *testing.T) {
	_, sshPid := startBridgeWithFakeSSH(t)
	time.Sleep(1500 * time.Millisecond)
	if !alive(sshPid) {
		t.Fatalf("the tunnel (pid %d) died while the bridge was still running", sshPid)
	}
}
