# Embedded Remote Debug

A Claude Code **plugin** (single skill) for debugging a **Cortex-M** embedded
target live on real hardware, when the agent has no direct board access —
everything goes through the GDB remote protocol over a socket.

It is **probe-, target-, and IDE-agnostic**: it drives raw GDB via
non-interactive script files against whichever GDB server the project uses —
**OpenOCD** or **SEGGER JLinkGDBServer** — reached either directly
(`localhost:<port>`) or through a **reverse SSH tunnel** when the agent runs
on a different machine from the probe. The methodology carries across STM32,
Silicon Labs EFM32, Nordic nRF, and any other SWD/JTAG MCU.

## What it does

1. **Reads the project's own server + load scripts first** to learn which
   server, chip, and ports are actually in use — never assumes.
2. **Verifies the link is genuinely live** (TCP probe + a real GDB attach)
   before every attempt, and troubleshoots with the user when it isn't,
   distinguishing "server not running" from "server up but can't reach the
   chip" from "physical-layer problem only your hands can fix."
3. **Connects, loads, and reproduces** via scripted GDB — single-image or
   multi-image (bootloader + application) — capturing output to a file.
4. **Diagnoses evidence-first**: live register/memory state and a known-good
   reference over reasoning-from-source; Cortex-M fault decoding (`xPSR`,
   CFSR/HFSR), raw-stack call-chain reconstruction, hardware-watchpoint
   register tracing, and live register patching to prove a fix before writing
   it.
5. **Fixes, rebuilds, re-verifies** the exact reproduction, then runs the
   full regression — and reports with the actual evidence.

The companion tool `tools/debug-bridge` carries the board's **serial console**
and the **GDB port** to a remote agent over one self-made reverse SSH tunnel,
and with `-local` opens a **tmux window on your machine** the agent can type
into and read from — so the same skill helps with ordinary programs on your
own box, not just firmware, while you watch every command land.

## GDB server quick reference

| | OpenOCD | SEGGER JLinkGDBServer |
|---|---|---|
| GDB port (default) | `3333` | `2331` |
| Telnet/monitor port | `4444` | `2333` |
| Reset **and halt** | `monitor reset halt` | `monitor reset` |
| Halt / run | `monitor halt` / `monitor resume` | `monitor halt` / `monitor go` |

Ports and `-device`/target come from the project's server script — read it
per run (they vary even between build variants).

## Root-cause categories it checks early

One-step read lag in byte-at-a-time full-duplex (SPI) drivers · clock margin
vs correctness · retry loops that abort instead of retrying · re-running
one-time init on already-active hardware (bootloader→app handoff) · fault
handlers never actually linked (weak-alias / static-archive extraction) ·
stuck external peripherals that only a power cycle clears.

## Install

This repo is a Claude Code marketplace. Add it and enable the plugin:

```
/plugin marketplace add mdodkins/claude-remote-debugging-skill
/plugin install embedded-remote-debug@mdodkins-remote-debugging
```

Or in `settings.json`:

```json
{
  "enabledPlugins": { "embedded-remote-debug@mdodkins-remote-debugging": true },
  "extraKnownMarketplaces": {
    "mdodkins-remote-debugging": {
      "source": { "source": "github", "repo": "mdodkins/claude-remote-debugging-skill" }
    }
  }
}
```

## License

MIT
