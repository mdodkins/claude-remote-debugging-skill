# serial-capture

A small, capture-only serial console reader that **survives board resets**. It
streams a board's UART console to stdout, an optional logfile, and an optional
TCP port — timestamping every line and reconnecting automatically when the board
resets and the USB serial re-enumerates. Each connect/disconnect is written as a
`[serial-capture]` marker line, so a reset is obvious in the log instead of a
silent gap.

It never writes to the port (capture only).

## Why

When the debugging agent runs on a different machine from the board, the serial
console has to reach it over the network — the same problem the GDB server's port
solves with a reverse SSH tunnel. `serial-capture` bridges the serial device to a
TCP port; you reverse-tunnel that port to the agent, which reads the live stream.
Plain `socat`/`cat` can do the bridging, but they die the moment the board resets
and the device re-enumerates — exactly when you're power-cycling during bring-up.
This handles that.

## Build

```bash
cd tools/serial-capture
go build -o serial-capture .
```

Produces a single static binary (Linux/macOS/Windows).

## Usage

```bash
# Capture to a timestamped logfile and serve the live stream on TCP :5555
./serial-capture -port /dev/ttyUSB0 -baud 115200 -log board.log -listen :5555
```

| Flag | Default | Meaning |
|------|---------|---------|
| `-port` | (required) | Serial device path. Prefer a stable `/dev/serial/by-id/...` path — it survives re-enumeration; a bare `/dev/ttyACMx` can change number on reset. |
| `-baud` | `115200` | Baud rate (8N1). The C-Sound console is 115200 8N1. |
| `-log` | (none) | Also append captured lines to this file. |
| `-listen` | (none) | Also serve the live stream on this TCP address, e.g. `:5555`. Multiple clients may connect; each gets lines from the moment it connects. |
| `-reconnect` | `1s` | Delay between reconnect attempts. |
| `-poll` | `500ms` | How often to check for the device to reappear. |

## Reaching it from a remote agent

Run the capture where the board is plugged in, exposing a TCP port, and
reverse-tunnel that port to the agent's host (same mechanism as the GDB port):

```bash
# on the machine with the board:
./serial-capture -port /dev/serial/by-id/usb-...-if00 -listen 127.0.0.1:5555 -log board.log &
ssh -N -R 5555:localhost:5555 <agent-host>

# on the agent host:
nc localhost 5555        # live, timestamped console — reconnect markers included
```

## Notes

- If the device path changes on re-enumeration (`ttyACM0` -> `ttyACM1`), point
  `-port` at the `/dev/serial/by-id/...` symlink, which stays stable.
- Capture only: there is no path from the network side back to the board.
