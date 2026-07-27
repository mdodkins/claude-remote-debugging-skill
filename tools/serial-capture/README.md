# serial-capture

A small, capture-only serial console reader that **survives board resets** and
can **bring up its own reverse SSH tunnel**. It streams a board's UART console to
stdout, an optional logfile, and (optionally) a remote host — timestamping every
line, reconnecting automatically when the board resets and the USB serial
re-enumerates, and replaying recent scrollback to anyone who connects.

Each connect/disconnect is written as a `[serial-capture]` marker line, so a
reset is obvious in the log instead of a silent gap. It never writes to the port
(capture only).

## Build

```bash
cd tools/serial-capture
go build -o serial-capture .
```

Produces a single static binary (Linux/macOS/Windows), Go 1.18+.

## Usage — one command, remote agent

To let an agent on another machine read the console, point `-tunnel` at the
agent's host. serial-capture opens the port **and brings up the reverse SSH
tunnel itself** — you just get ssh's normal password prompt (or it uses your
keys/agent silently):

```bash
serial-capture -port /dev/serial/by-id/usb-...-if00 -tunnel you@agent-host.com
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
serial-capture -port /dev/serial/by-id/... -tunnel you@agent-host.com -gdb-port 2331
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
serial-capture -port /dev/ttyUSB0 -baud 115200 -log board.log -listen :5555
```

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

## Notes

- If the device path changes on re-enumeration (`ttyACM0` -> `ttyACM1`), point
  `-port` at the `/dev/serial/by-id/...` symlink, which stays stable.
- Both ends of the tunnel are pinned to IPv4 `127.0.0.1`, avoiding `localhost`
  IPv4/IPv6 mismatches.
- Capture only: there is no path from the network side back to the board.
