# debug-bridge

A small bridge between a developer's machine and a remote agent. It has two
jobs, usable together or alone:

- **Serial console capture** (`-port`): a capture-only UART reader that
  **survives board resets** and can **bring up its own reverse SSH tunnel**.
- **Local window** (`-local`): a tmux window on this machine that the agent can
  type into and read from, for developing and debugging ordinary programs here
  while you watch.

## Serial console

A capture-only serial console reader that survives board resets. It streams a board's UART console to
stdout, an optional logfile, and (optionally) a remote host — timestamping every
line, reconnecting automatically when the board resets and the USB serial
re-enumerates, and replaying recent scrollback to anyone who connects.

Each connect/disconnect is written as a `[debug-bridge]` marker line, so a
reset is obvious in the log instead of a silent gap. It never writes to the port
(capture only).

## Build

```bash
cd tools/debug-bridge
go build -o debug-bridge .
```

Produces a single static binary (Linux/macOS/Windows), Go 1.18+.

## Usage — one command, remote agent

To let an agent on another machine read the console, point `-tunnel` at the
agent's host. debug-bridge opens the port **and brings up the reverse SSH
tunnel itself** — you just get ssh's normal password prompt (or it uses your
keys/agent silently):

```bash
debug-bridge -port /dev/serial/by-id/usb-...-if00 -tunnel you@agent-host.com
```

It wraps the system `ssh`, so your `~/.ssh/config`, known_hosts, keys and
password all work as usual. Then on the agent host:

```bash
nc localhost 5555        # live, timestamped console — with scrollback + reset markers
```

To give the agent the **debug probe too**, add `-gdb-port` — the same tunnel then
also forwards the J-Link GDB port, so one command covers both channels (you still
start the GDB server itself, since it needs the probe):

```bash
# on the machine with the board:
sh start-gdb-server.sh &                                   # J-Link GDB server on 2331
debug-bridge -port /dev/serial/by-id/... -tunnel you@agent-host.com -gdb-port 2331
# agent side: nc localhost 5555 (console) and gdb → localhost:2331 (registers)
```

It **warns clearly** if something's wrong rather than failing silently: ssh can't
connect / auth fails, the remote port is already in use (via
`ExitOnForwardFailure`), or the serial port is missing/busy. If the tunnel drops,
capture keeps running locally and a warning is logged.

## Usage — local only

Without `-tunnel` it just captures locally (stdout + optional log), and can serve
a local TCP port you tunnel yourself:

```bash
debug-bridge -port /dev/ttyUSB0 -baud 115200 -log board.log -listen :5555
```

## Usage — local window (`-local`)

`-local` opens a tmux window on this machine with a shell started in the
current directory, and serves it on `127.0.0.1:5556`: every line received on
that port is typed into the window, and the window's output streams back
(cleaned of colour codes, with scrollback for late joiners). Inside tmux the
window appears in your current session; outside, a detached session
`debug-bridge` is created (`tmux attach -t debug-bridge`, or pass `-attach`).
With `-tunnel` the port rides the same reverse tunnel as the serial console:

```bash
cd ~/my-project
debug-bridge -local -tunnel you@agent-host.com                 # window only
debug-bridge -local -port /dev/serial/by-id/... -gdb-port 2331 -tunnel you@agent-host.com   # all three channels
```

The agent side uses the built-in client — one command in, its output out, with
the prompt pattern telling it when the command has finished:

```bash
debug-bridge -connect 127.0.0.1:5556 -cmd 'make' -until '\$ $' -timeout 120s
debug-bridge -connect 127.0.0.1:5556 -cmd 'python3' -until '>>> $'      # start a REPL...
debug-bridge -connect 127.0.0.1:5556 -cmd 'import sys; sys.version' -until '>>> $'   # ...and talk to it
debug-bridge -connect 127.0.0.1:5556 -cmd '^C'                          # control keys: ^C ^D ^Z ^L
debug-bridge -connect 127.0.0.1:5556                                    # just watch (scrollback + live)
```

Exit status: `0` when the prompt matched or the window went `-quiet`, `124` when
`-timeout` passed first (the command is still running — connect again to keep
reading). Plain `nc 127.0.0.1 5556` works too: type a line, see the output.

**Security:** the window is a shell with your privileges. The port is bound to
loopback on both ends, so only processes on this machine and on the agent host
can reach it — but anyone on the agent host who can connect to its loopback can
type into your shell. Run it when you want the agent working on your machine,
and stop it (Ctrl-C) when you don't; stopping closes the window it opened.

## Flags

| Flag | Default | Meaning |
|------|---------|---------|
| `-port` | (required) | Serial device path. Prefer a stable `/dev/serial/by-id/...` path — it survives re-enumeration; a bare `/dev/ttyACMx` can change number on reset. |
| `-baud` | `115200` | Baud rate (8N1). The C-Sound console is 115200 8N1. |
| `-tunnel` | (none) | SSH target, e.g. `you@host`. Brings up a reverse tunnel to it and serves the stream there. Implies serving on `127.0.0.1:<tunnel-port>`. |
| `-tunnel-port` | `5555` | TCP port used on both ends of the serial tunnel. |
| `-gdb-port` | `0` | Also forward this port through the same tunnel, e.g. `2331` for the J-Link GDB server, so the agent gets **both** the serial console and the debug probe. `0` = serial only. |
| `-log` | (none) | Also append captured lines to this file. |
| `-listen` | (none) | Local-only: serve on this TCP address, e.g. `:5555` (ignored when `-tunnel` is set). |
| `-scrollback` | `200` | Lines of recent history replayed to a newly-connected client (so connecting mid-session shows context, not a blank screen). `0` disables. |
| `-reconnect` | `1s` | Delay between serial reconnect attempts. |
| `-poll` | `500ms` | How often to check for the device to reappear. |
| `-local` | off | Open a tmux window on this machine for the agent (shell starts in the current directory). |
| `-local-port` | `5556` | TCP port for the window, both ends of the tunnel. |
| `-local-name` | `agent` | Window name. |
| `-local-session` | `debug-bridge` | Session created when not already inside tmux. |
| `-local-dir` | (cwd) | Directory the window's shell starts in. |
| `-local-shell` | (tmux default) | Command run in the window instead of the default shell. |
| `-attach` | off | Outside tmux: attach this terminal to the window once the tunnel is up; detach with prefix-d, the bridge keeps running. |
| `-connect` | (none) | **Client mode:** address of a `-local` window, e.g. `127.0.0.1:5556`. |
| `-cmd` | (none) | Client: line to type (omit to just stream output). |
| `-until` | (none) | Client: stop when an output line matches this regexp (the prompt). |
| `-quiet` | `2s` | Client: stop after this much silence (`0` = never). |
| `-timeout` | `60s` | Client: give up this long after sending (exit 124). |

## Notes

- If the device path changes on re-enumeration (`ttyACM0` -> `ttyACM1`), point
  `-port` at the `/dev/serial/by-id/...` symlink, which stays stable.
- Both ends of the tunnel are pinned to IPv4 `127.0.0.1`, avoiding `localhost`
  IPv4/IPv6 mismatches.
- Serial capture is capture-only: there is no path from the network side back
  to the board. The `-local` window is the opposite by design — see Security.
