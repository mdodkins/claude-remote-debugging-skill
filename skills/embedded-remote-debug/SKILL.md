---
name: embedded-remote-debug
description: Debug a Cortex-M embedded target (STM32, EFM32/Silicon Labs, nRF, or any SWD/JTAG MCU) live over a GDB server — OpenOCD or SEGGER JLinkGDBServer — reached at localhost:<port>, whether directly (probe on the same machine) or through a reverse SSH tunnel (agent on a remote box). Use when the user reports a crash, hang, or unexpected on-target behavior and asks you to debug it, or mentions "the debug tunnel" / "debug on hardware" / a J-Link or ST-Link probe. Reads the project's own gdb-server and load scripts first, verifies the link is live, walks the user through troubleshooting if it's not, then applies a systematic, evidence-first root-cause methodology instead of guessing from source alone.
---

# Embedded Remote Debug

Real-hardware debugging of a Cortex-M target through a GDB server, when the
agent has no direct access to the board — everything goes through the GDB
remote protocol over a socket, so verifying the link is real and live is not
optional setup, it's the first diagnostic step.

**This is probe-, target-, and IDE-agnostic by design.** It drives raw GDB
(`gdb-multiarch`, or the project's vendor `arm-none-eabi-gdb`) via
non-interactive script files over `target remote` / `target
extended-remote` — not a VS Code launch config or any GUI frontend. That's
the same GDB/MI protocol a graphical debugger uses underneath, just scripted
instead of clicked: the only option for an agent (no GUI to interact with),
and it works identically whatever the user has configured locally. Don't
assume or require a `cortex-debug`/Ozone/vendor-IDE setup exists.

**Two things vary by project and must be read, never assumed:**

1. **The GDB server.** Either OpenOCD or SEGGER's JLinkGDBServer. They differ
   in default ports and in `monitor` command dialect (table below). The
   project starts one of them from its own script — read that script to learn
   which server, which chip (`-device`/target cfg), and which ports it
   actually uses.
2. **The transport.** The server runs where the debug probe (ST-Link,
   J-Link, CMSIS-DAP…) is physically plugged in. Its GDB port becomes
   `localhost:<port>` for the agent either **directly** (agent and probe on
   the same machine — e.g. a local two-terminal flow) or through a **reverse
   SSH tunnel** (agent on a remote box, probe on the user's laptop). The
   debugging methodology below is identical either way; only Step 1 differs.

### GDB server quick reference

| | **OpenOCD** | **SEGGER JLinkGDBServer** |
|---|---|---|
| GDB port (default) | `3333` | `2331` |
| Telnet/monitor port | `4444` | `2333` |
| (SWO port) | — | `2332` |
| Reset **and halt** | `monitor reset halt` | `monitor reset` (resets *and* halts by default) |
| Halt / run | `monitor halt` / `monitor resume` | `monitor halt` / `monitor go` |
| Health probe (telnet) | `version` | `?` or just connect |

Read the actual ports from the project's server script; these are only the
defaults. `monitor` sub-commands are passed through to whichever server is
running, so the *dialect* is the server's, not GDB's — using an OpenOCD
`monitor` verb against a J-Link server (or vice-versa) silently does nothing
or errors.

## When to use

- The user reports a crash, hang, silent failure, or "not seeing X" on real
  hardware and wants it debugged, not just reasoned about.
- The user says "I've started the tunnel / the GDB server" / "debug this on
  hardware" / asks you to check something live rather than in a host-side
  test or emulator.
- A bug can't be reproduced or explained from source/static analysis alone —
  the only way to know is to look at real register/memory state on the
  device.

Don't use this for anything a host unit test or static analysis can answer
faster. Reaching for a live hardware session when the bug reproduces in a
fake/mock is slower and burns the user's hardware time for nothing.

## Step 1 — Establish the link (server + transport)

The GDB server is started **where the probe is plugged in** — you cannot
start it yourself if that's the user's machine. Ask the user to run the
project's server script if they haven't already, and **read that script
first** to confirm the actual server, device, and ports — don't assume:

```bash
# Example — OpenOCD-based project, often wrapped in a tunnel script:
./scripts/debug-tunnel.sh [ssh-host-alias]

# Example — SEGGER J-Link project (e.g. csound: EFM32 Wonder Gecko):
#   /opt/SEGGER/JLink/JLinkGDBServer -select USB=0 -device EFM32WG990F256 \
#       -endian little -if SWD -speed 4000 -noir -noLocalhostOnly \
#       -nologtofile -port 2331 -SWOPort 2332 -TelnetPort 2333
./build/start-gdb-server.sh
```

Note what the script tells you: the **`-device`** (e.g. `EFM32WG990F256`
vs `EFM32WG380F256` — it varies even between build variants of one project,
so read it per run), the **`-if`** (SWD/JTAG), and the **ports**. A J-Link
server here listens on `2331`, not OpenOCD's `3333`.

**Transport.** If the agent is on a different machine from the probe, the
server's GDB port must be reachable as `localhost:<port>` on the agent's
side. Two ways:

- **Reverse SSH tunnel** (the common remote case): a wrapper script starts
  the server locally *and* opens `ssh -N -R <port>:localhost:<port>
  <host-alias>` so the port is forwarded back to the remote box running the
  agent. If the project's server script doesn't already do this, the user
  runs the tunnel alongside it.
- **Direct** (agent and probe co-located): the server already listens on
  `localhost:<port>` — nothing extra to do. `-noLocalhostOnly` on a J-Link
  server means it listens on all interfaces, which also makes it reachable
  over a plain LAN/port-forward without a tunnel.

## Step 2 — Verify the link is actually live

Don't assume it's up just because the user said so, and don't assume it's
still up from earlier in the same conversation — it drops silently (SSH
session closed, server crashed, probe unplugged) with no notification.
**This isn't a one-time check.** In a long session the link will drop
mid-session too — moving probes for a logic-analyzer capture, a laptop
sleeping, a flaky SSH link — with zero warning before your next GDB
invocation just hangs or refuses. Re-verify before *every* attempt; if a GDB
invocation times out or errors in a way that doesn't match the failure you
expected, check the link before assuming it's a target-side problem.

**Quick reachability check first** (cheap; catches "never started" or
"dropped"). A bare bash TCP probe is faster than spawning `nc` and needs no
`nc` install — substitute the real GDB port for your server (`3333` OpenOCD,
`2331` J-Link):

```bash
PORT=2331   # or 3333 for OpenOCD — read it from the server script
timeout 5 bash -c "echo > /dev/tcp/localhost/$PORT" 2>&1 && echo reachable || echo "NOT reachable"
```

`Connection refused` means the link isn't up — go to Troubleshooting.

**Real test: an actual GDB attach**, since a TCP port can be open while the
server itself is wedged (stale tunnel, crashed probe driver). Use the
correct `monitor` reset verb for the server:

```bash
# J-Link:
timeout 15 gdb-multiarch -batch \
    -ex "target remote localhost:2331" \
    -ex "monitor reset" \
    -ex "info registers pc sp" 2>&1
# OpenOCD: swap to  localhost:3333  and  "monitor reset halt"
```

A real target replies with a halt message and sane `pc`/`sp` values
(J-Link: `Resetting target`; OpenOCD: `[stm32u5x.cpu] halted due to
debug-request`). If this hangs or errors, the port is up at the TCP level
but the server/probe isn't functional — troubleshoot before going further.

## Troubleshooting (link not connecting)

Work through these with the user — most needs checking *on their side*, you
can only observe from the remote end:

1. **Is the server/tunnel script actually still running?** A tunnel's `ssh
   -N` blocks; a foreground JLinkGDBServer stays attached to the terminal.
   If their terminal shows a prompt back, it exited — check for an error
   just above it. Common:
   - OpenOCD: `OpenOCD failed to start. Check USB/ST-Link connection…` →
     probe not plugged in / not recognized / already held by another process
     (a second server, their IDE's debugger). Unplug/replug; check nothing
     else has the probe.
   - J-Link: `Could not connect to target` / `Cannot connect to J-Link` →
     probe not found, wrong `-device`, or another JLinkGDBServer/Ozone
     instance already owns the probe.
   - A permissions error opening the USB device → udev rules / group
     membership on their machine (out of your control beyond flagging it).
2. **Test SSH itself, independent of the tunnel** (remote case). Ask them to
   run `ssh <host-alias> echo ok` in a separate terminal. If that fails it's
   a plain SSH/network problem (wrong alias, key not loaded, VPN down) —
   nothing to do with the debug server. Fix SSH first.
3. **Check the server process is actually running locally.** `ps aux | grep
   -E 'openocd|JLinkGDBServer'`. If it's not there, it died after starting —
   re-run and watch for the failure this time.
4. **Check port collisions.** Another debug session (their IDE, a stuck
   tunnel, an Ozone) already bound to the GDB/telnet port locally makes the
   new server fail to bind. `lsof -i :2331 -i :2333` (J-Link) or `:3333
   :4444` (OpenOCD) on their machine; kill anything stale.
5. **Once fixed, re-verify from your side with Step 2** — the actual GDB
   attach, not just the port check. Don't take "should be working now" as
   confirmation.

**"Server running but can't reach the chip" is a distinct failure** from
"server isn't running" — don't conflate them. If `monitor` commands fail but
you can't tell why, query the server's telnet port directly for a clearer
message than GDB gives:

```bash
# OpenOCD (4444):
timeout 5 bash -c 'exec 3<>/dev/tcp/localhost/4444; echo "version" >&3; cat <&3'
# J-Link (2333): connect and send "?" — a healthy server responds with its banner.
```

OpenOCD's `Open On-Chip Debugger <version>` with no further error means
OpenOCD is healthy. Errors like `Examination failed`, `jtag status contains
invalid mode value`, or a garbage adapter speed (`Unable to match requested
speed 480 kHz, using 281485328 kHz`) mean the server is fine but **can't talk
to the probe or target over SWD/JTAG** — a physical-layer problem: the debug
connector isn't seated, or (the garbage-speed case) the probe's USB link
glitched, commonly from unplugging/replugging a shield or board while the
probe stayed connected.

**When the diagnosis points to something only the user's hands can fix, say
so directly and stop there — don't offer to keep polling.** Retrying a GDB
attach or re-probing telnet from your side cannot fix a physical connector or
a wedged USB device; only the user, physically present, can. State the
specific action and wait for them to confirm it's done, rather than "want me
to keep checking?" — that puts a decision that was never yours back on them.

For the USB/probe-glitch case, a half-killed server can leave a stray process
still holding the USB device open, which silently defeats a fresh start even
with nothing physically wrong — try software-only recovery first:

**First pass (software only, no cable unplugging):**
1. Stop the running server/tunnel script (Ctrl+C).
2. Check for a *stray* server still running afterward — Ctrl+C on a wrapper
   doesn't guarantee its backgrounded child died: `ps aux | grep -E
   'openocd|JLinkGDBServer'`.
3. If one's there, kill it (`kill <pid>`, `kill -9` if unresponsive) —
   otherwise it keeps the probe's USB device open and the next server fails
   to acquire it.
4. Start the server script again; confirm it reports running with no error.
5. Re-verify from your side (Step 2's GDB attach, not just the port check).

**Second pass, only if that didn't fix it:** unplug/replug the probe's USB
cable, then repeat. Cable-cycling usually isn't needed — killing a stray
process and restarting cleanly resolves most cases — so don't lead with it.

## Step 3 — Build the target

Use the project's own build scripts — read what exists first (a CMake
preset, a `scripts/build-*.sh`, a `make` target, a vendor SDK build). Prefer
a single targeted build over a full-matrix rebuild; hardware sessions are
exactly where slow no-op rebuilds waste limited hardware time. If only a
slow full build exists and you'll iterate a lot, it's worth adding a
targeted one rather than reaching for a bare `docker run`/hand-rolled
compile.

**Never run two builds concurrently into the same build directory.** They
race and corrupt each other's output (spurious "undefined reference" errors
that look like real bugs but aren't). If a background build/CI job is still
running, wait for it before starting another into the same `build/…` dir.

The artifact GDB loads is the ELF (`.elf`) or ARM image (`.axf` — same
format, ARM's naming); match whatever the project produces and its load
scripts reference.

## Step 4 — Connect, load, and reproduce

Always drive GDB via a **non-interactive script file**, not an interactive
session — this tool-calling context can't answer prompts, and output needs
capturing to a file you can grep, not streaming live. Reuse the project's
own `gdbinit`/load scripts where they exist (read them — they encode the
right connect/reset/load sequence and any multi-image layout).

**Single image** (typical), J-Link ports shown — swap `2331`→`3333` and
`monitor reset`→`monitor reset halt` for OpenOCD:

```bash
cat > /tmp/gdb_session.gdb << 'EOF'
set confirm off
set pagination off
target remote localhost:2331
set remote memory-read-packet-size 65535
set remote memory-write-packet-size 65535
monitor reset
load
monitor reset
break Default_Handler
continue
EOF
timeout 45 gdb-multiarch -x /tmp/gdb_session.gdb path/to/target.elf > /tmp/gdb_output.log 2>&1
echo "exit: $?"; cat /tmp/gdb_output.log
```

**Multi-image** (bootloader + application, e.g. csound's `gdbinit-all`): load
each image, then set up symbols for both — the running symbol file is the
one you break in, plus an offset-loaded second set:

```gdb
target remote localhost:2331
monitor reset
file bootloader.axf
load                              # bootloader at 0x0000
file csound.axf
load                              # application at its base (e.g. 0x4000)
symbol-file bootloader.axf        # debug the bootloader…
add-symbol-file csound.axf 0x4000 # …with the app's symbols also resolvable
monitor reset                     # start from the reset vector (bootloader)
break main
continue
```

To debug the application in isolation without a bootloader present, break at
its *real* entry (not the bootloader's `main`) — e.g. `break *0x42E8` — since
`main` may be ambiguous or the wrong image's.

Notes learned the hard way (all still apply, any server/target):
- `set remote memory-read/write-packet-size 65535` matters a lot over an SSH
  tunnel — default packet sizes mean far more round-trips, each paying the
  tunnel's latency.
- Redirect to a file (`> log 2>&1`) and `cat`/`grep` afterward. Piping
  straight through `tail`/`grep` can lose output if `timeout` kills the
  process mid-flush.
- `break Default_Handler` catches *every* unimplemented weak vector-table
  entry at once (they all alias one address) — a useful first catch-all when
  you don't yet know which fault fired.
- A single `continue` after load + reset runs the whole boot sequence; add
  breakpoints or `finish`/`next` once you know roughly where the interesting
  behavior is.
- **Line numbers drift on every source edit.** A `break file.cpp:153` set
  before you edited that file can silently land on the wrong statement (or
  fail to resolve) after you add/remove lines anywhere above it — including
  your own comments. Re-`grep` the current line number immediately before
  *every* GDB invocation that follows a source edit; don't reuse a number
  from a script written even one edit ago.
- **When you need the target to keep running untethered** — for an external
  tool to observe it (a logic-analyzer capture, or the user watching
  serial/LED behavior) — hand control back explicitly rather than leaving
  GDB attached and idle:
  ```gdb
  monitor go        # J-Link ("monitor resume" on OpenOCD); or "monitor reset run" on OpenOCD to reset+run
  detach
  quit
  ```
  A plain `continue` still holds the connection open and can add polling
  overhead; `monitor go`/`monitor reset run` + `detach` genuinely releases
  the target to run on its own.

**GDB scripting reliability gotchas** (all hit and confirmed, not
theoretical):
- `commands` blocks attached to a **breakpoint** (`break file:line` then
  `commands`/`silent`/`printf …`/`continue`/`end`) auto-continue reliably in
  batch (`-x script.gdb`) mode. The same pattern on a **watchpoint**
  (`watch`/`rwatch`/`awatch`) did *not* reliably fire its `commands` block in
  batch — the target just stopped at the first hit and sat there. For
  watchpoint tracing, use explicit sequential `continue` + `printf` pairs
  instead of relying on `commands` to auto-continue.
- **Don't mix `finish` with a breakpoint `commands` block that also
  `continue`s.** This combination hung repeatedly (exact mechanism unpinned,
  but consistent enough to avoid). Prefer plain breakpoint-hit + `continue`
  tracing; use `finish` standalone.
- **Avoid inferior function calls** (`p some_object.SomeMethod(args)`, which
  makes GDB execute code on the live target) beyond the most trivial case —
  this hung on real hardware. Prefer reading/writing memory directly
  (`p/x *(uint32_t*)0xADDR`, `set variable *(uint32_t*)0xADDR = val`).
- **`p someArray[i]` can fail in batch mode** on `-O0` builds with `Could not
  find operator[]` for `std::array`/similar, even though the same expression
  works interactively. Print the whole container (`p someArray` / `p/x
  someArray`) instead of indexing.
- **Verify raw peripheral register addresses against the project's own
  CMSIS/vendor header before hardcoding hex in a GDB script.** A single
  mixed-up offset (reading adjacent register A when you meant B) produces a
  plausible wrong number with no error. Cross-reference the exact `typedef
  struct { … } SomeTypeDef;` layout for every field's offset; when in doubt,
  print the whole peripheral through its named type (`p
  *(USART_TypeDef*)0x40013800`, using *your* chip's base from *its* header)
  rather than individual hardcoded field addresses — GDB then resolves every
  field from the same struct the compiler used, removing the manual-arithmetic
  failure mode.

**Hardware watchpoints are a powerful, line-number-independent tracing tool
that's easy to forget.** Cortex-M's DWT unit supports a handful of hardware
watchpoints (`watch`/`rwatch`/`awatch` on a memory address) — unlike
breakpoints, these fire on actual bus access to an address, regardless of
which instruction or source line caused it. Extremely effective for tracing
*exactly* which instructions touch a given peripheral register during a
black-box call (a vendor HAL function) without reading its source line by
line:

```gdb
watch  *(uint32_t*)0x40013000    # write watchpoint on a control register
watch  *(uint32_t*)0x40013020    # write watchpoint on a data-out register
rwatch *(uint32_t*)0x40013030    # read watchpoint on a data-in register
continue
# (repeat continue + inspect state at each hit)
```

This is how a multi-day "everything about this driver's config and sequencing
looks identical to the working reference" investigation got resolved in one
shot — a watchpoint-based instruction trace of the *actual* working
reference's execution (not its C source, not its disassembly read by eye)
showed the exact register sequence and status values at a register read,
comparable byte-for-byte against the same live trace on the non-working code.

## Step 5 — Diagnose: evidence over theory

This is the part that actually matters. Don't reason from memory about what a
register or protocol "should" do — check the project's own CMSIS headers, the
reference vendor implementation (ST HAL/LL, Silicon Labs emlib, Nordic
nrfx, … whatever's in the repo or a sibling checkout), and the live hardware
state, in that order of trust.

**A landed breakpoint's symbol name can be misleading.** If it's a weak alias
shared by multiple vector-table entries (all pointing at `Default_Handler`),
GDB may report *any* of the aliased names — not necessarily which interrupt
fired. Check the real exception source (these core registers are identical
across all Cortex-M vendors — ARMv7-M/v8-M architectural, not chip-specific):

```gdb
p $xpsr & 0x1ff
```

The low 9 bits of xPSR are the active exception number (3=HardFault,
5=BusFault, 6=UsageFault, 11=SVCall, 14=PendSV, 15=SysTick, 16+=IRQn+16).
Cross-check the fault status registers (SCB block at `0xE000ED00`, same on
every Cortex-M — but confirm bit *meanings* in *this* project's CMSIS
header):

```gdb
x/1xw 0xE000ED28   # CFSR (MemManage/Bus/UsageFault status, packed)
x/1xw 0xE000ED2C   # HFSR (HardFault status)
```

**Don't trust the automatic exception stack frame blindly.** On a stack
overflow specifically (Armv8-M STKOF), the CPU may skip the normal push-on-
entry to avoid making the overflow worse — reading `$msp` right at the fault
handler can give garbage, not a real R0-R3/R12/LR/PC/xPSR frame. If the
values don't look like plausible code/data addresses, don't trust them.

**When the automatic frame is unreliable, walk the raw stack instead.** At
`-O0`, GCC keeps R7 as a frame pointer: every function's prologue leaves a
`[saved R7][return address]` pair at a fixed offset. Dump a wide stack region
and look for the pattern (a plausible-RAM-address word immediately followed
by a plausible-FLASH-address word):

```gdb
x/NNNxw <low_stack_address>
```

Then resolve every candidate return address to a function name — this
reconstructs the actual call chain even when a clean `bt` isn't possible
(use the project's toolchain prefix, e.g. `arm-none-eabi-addr2line`):

```bash
arm-none-eabi-addr2line -f -e target.elf 0x08001234 0x08001456 …
```

**"The symbol isn't linked" is a real, recurring class of bug on embedded
targets**, not just a debugging inconvenience — and it hides two ways:
1. A weak vector-table alias (`.weak Foo; .thumb_set Foo,Default_Handler`)
   silently wins over a real implementation living in a *static archive*,
   because archive-member extraction only happens when something creates a
   genuine undefined-symbol reference — and a weak alias that already
   "resolves" the symbol never creates one. `-Wl,-u,Foo` does nothing here
   for the same reason. Force-reference a *different*, non-weak symbol only
   defined in the same translation unit as the real implementation (e.g. a
   private helper the real handler calls) — that genuinely has no fallback,
   so the linker pulls the whole `.o` in, and the real strong definition
   comes along, correctly overriding the weak alias.
2. Some vendor SDKs `#define` the public API name to an internal one
   (`#define fx_foo _fx_foo`, FileX-style). If GDB says `Function "fx_foo"
   not defined` even though you're clearly calling it, check `nm` for the
   real underscore-prefixed symbol and breakpoint that — a naming mismatch,
   not a bug.

**Test hypotheses on live hardware before touching source.** If you suspect a
specific register value (a clock divider, a timing constant) is the problem,
patch it live and re-run *before* writing the fix:

```gdb
break SomeFunction
continue
set variable *(unsigned int*)0xADDR = (*(unsigned int*)0xADDR & ~MASK) | NEW_VALUE
finish
# check the return value / behavior right there
```

Dramatically faster than a rebuild-reflash-reverify cycle per guess, and the
eventual source fix is something you've *proven* works, not something that
merely looks plausible.

**GDB decodes typed return values for you.** `finish` after breaking inside a
function that returns something like `std::expected<T, E>` prints the actual
variant contents (`_M_has_value`, the value or the error enumerator) — read
it, don't just check whether `finish` completed.

**When comparing against a known-working reference, distrust the reference's
own source comments as much as your own assumptions.** A vendor HAL's C source
can say one thing while its *actual compiled, running* behavior does another:
a real case — a reference driver's C source explicitly set a field to
`DISABLE`, strongly implying a register bit should read 0; a live register
dump of that same reference running showed the bit set to 1 regardless. When
source-reading and live register state disagree, trust the live read, every
time, for both your code and the reference's.

**Verify you're comparing against the right reference before trusting any of
it.** If a project has multiple similar branches/variants (a proven merged
one and an abandoned/in-progress one), building the wrong one wastes hardware
time and can produce a false "even the reference fails" conclusion. Check the
branch you're building is genuinely the known-good one — merge-base against
the commit you believe the working state descends from, or just ask.
`git log --all --grep=<keyword>` against the reference's full history is worth
doing early if you suspect "someone already hit and fixed this exact bug" —
it can surface the actual fix commit (with a detailed, evidence-based
message) far faster than re-deriving the root cause.

**A stable, deterministic "wrong" register value doesn't mean the peripheral
or its configuration is broken.** If register/status-flag comparison against a
working reference shows everything matching bit for bit — same sequencing,
same timing, same flags at every check — but the data is still wrong, stop
comparing configuration and start comparing configuration **against the
transferred data itself**, byte by byte. A logic analyzer (or, short of that,
direct software tracing of every transmitted/received byte in sequence)
operates on a different axis than register inspection: it shows what genuinely
happened on the wire, independent of what the software believes happened. See
"one-transfer read lag" below — config-perfect, data-wrong is its signature,
and register comparison alone cannot surface it.

**Separate code-dependent symptoms from code-independent ones before chasing a
physical explanation.** A genuinely physical/electrical cause (bad solder
joint, marginal connector, signal integrity) cannot explain a symptom that
changes depending on which firmware runs on the exact same physical hardware
and pins — if swapping firmware changes the outcome, the cause is in
firmware/config, not the wire. Sanity-check any "maybe it's the hardware"
hypothesis against this before spending time on it (a scope probe, a cable
reseat, a board swap).

**Validate an exciting-looking anomaly against a known-good baseline before
trusting it.** A precise-looking pattern in a capture or trace (a value that's
exactly what a specific corruption would produce) is convincing evidence of a
real bug — but only if the same pattern *doesn't* also appear in a capture of
the known-working reference. If it does, it's more likely an artifact of the
capture/analysis tool itself (a fixed-size export buffer splitting one
continuous transaction into "packets," with whatever byte was mid-transfer at
the split reported as meaningful) than a real signal. Check the baseline
before getting attached to a striking finding.

**When someone (a colleague, the user) proposes a specific, testable
hypothesis, run the exact test rather than reasoning about whether it could be
true first.** The single most valuable moment in a real session this skill was
built from was a suggestion to "unconditionally read one extra byte and print
both results, regardless of what the first one says" — a 60-second test that
immediately settled a question hours of register-level theorizing hadn't. If a
concrete, cheap experiment is proposed, run it before debating it.

## Step 6 — Fix, rebuild, re-verify (real hardware, then full regression)

1. Apply the fix in source.
2. Rebuild via the project's scripts (Step 3).
3. Re-run the *exact* GDB verification that first demonstrated the bug —
   confirm it no longer reproduces, not just that the build succeeded.
4. Run the project's full CI/regression replica (not just the one preset you
   were iterating on) before considering it done. A fix checked only against
   the specific configuration you were debugging can easily regress something
   else silently.
5. Report findings with the actual evidence (register values, resolved call
   chain, before/after test results) — not just "should be fixed now". The
   person you're reporting to (and your future self) should see *why* you
   believe it, not just that you do.

## Common root-cause categories worth checking early

These recur often enough on embedded targets to rule in/out before a long
investigation, roughly in order of how cheap they are to check:

- **One-step read lag in a byte-at-a-time full-duplex exchange.** The single
  most valuable, hardest-won lesson this skill exists to pass on. In a manual
  (non-DMA, non-burst) SPI/similar driver that issues one physical transfer
  per logical byte, a read of the "response" register can come back describing
  the *previous* transfer's result rather than the one that just completed —
  deterministically, every single time, not an occasional race. Maximally
  confusing to chase via register inspection: every status flag looks healthy
  (transfer-complete, no error, no overrun), sequencing matches a known-good
  reference exactly, and the wrong value is *completely stable* across every
  retry and clock speed — because it isn't corruption, it's a consistent
  off-by-one in which transfer's result you're reading. Looks identical to
  "the peripheral is broken" or "the response never arrives," and survives
  extensive clock-speed/sequencing/config comparison with zero explanation,
  because none of those is the actual variable. **The cheap, decisive test:**
  at the point you read a response, unconditionally issue one extra dummy
  transfer and read *that* too, printing both — no theorizing, no rebuild if
  it's reachable via a live GDB patch first. If the second read is the value
  you expected and the first is the "stuck" one, this is the bug. The fix is a
  throwaway read to flush the lag once per command/response sequence, not a
  peripheral reconfiguration.
- **Clock margin, not clock correctness.** A nominal clock calculation landing
  exactly at a spec ceiling ("24 MHz, just under a 25 MHz limit") has zero
  real margin if the source is untrimmed (no CRS/PLL calibration) — it can
  drift over the line under real silicon/temperature variation even though the
  math looks fine. If something intermittently fails at a "should be within
  spec" rate, try a materially slower divider first — a five-minute live
  register patch (Step 5) that immediately confirms or rules out the whole
  category.
- **A retry/poll loop that aborts instead of retrying.** Skim any loop with a
  documented "N attempts, timeout-bounded" comment for whether the code inside
  actually matches — `if (!step()) { return error; }` inside a for-loop
  silently defeats retry logic that looks correct at a glance.
- **Re-running one-time initialization on already-active hardware.** If a
  driver's init is only ever exercised once at cold boot in the
  reference/production path, and your new test path calls it a second time on
  hardware that's already up, don't assume the init is robust to that — check
  whether skipping re-init when already initialized is the more correct fix
  than making re-init itself reliable. (Bootloader→application handoff is a
  classic place this bites: the app re-initializing a clock/peripheral the
  bootloader already brought up.)
- **Fault handlers that were never actually linked.** See the archive-
  extraction gotcha in Step 5 — a project can build and run "fine" for a long
  time with `HardFault_Handler`/`UsageFault_Handler` silently aliased to a
  no-op default, because nothing calls them by name until an actual fault
  occurs. If you're debugging a fault and it lands somewhere
  generic/uninformative, check this first.
- **A stuck external peripheral that only a real power cycle clears.**
  `monitor reset` (J-Link) / `monitor reset halt`/`run` (OpenOCD) resets the
  MCU (NRST) — it does *not* power-cycle external peripherals sharing the
  board's power rail (an SD card, a sensor, anything with its own internal
  state machine). If you suspect a peripheral has latched into a persistent
  bad state from an earlier buggy run and MCU-only resets aren't clearing it,
  ask the user to physically power-cycle the peripheral or the whole board,
  not just re-flash. Verify it's plausible first — if a known-working
  reference firmware succeeds immediately when flashed right after your
  firmware's failure, on the same un-power-cycled hardware, that's direct
  evidence there's no persistent latched state to clear, and the cause is
  elsewhere.

## Serial console capture (companion to the GDB link)

The board's UART/`printf` console is a **separate channel** from the GDB link —
GDB gives you registers, memory, and control; the serial console gives you the
firmware's own log (boot banner, config dump, runtime state). When the agent is
remote, that console has to reach it the same way the GDB port does: bridged to
TCP and reverse-tunnelled.

`tools/debug-bridge` (in this repo) does that, and — unlike a plain
`socat`/`cat` bridge — **survives board resets**: it reconnects automatically
when the board re-enumerates and marks every connect/disconnect in the stream, so
a power-cycle shows up as a clear marker instead of a silent gap. It is
capture-only (never writes to the port), replays recent scrollback to whoever
connects, and can **bring up the reverse SSH tunnel itself** — one command:

```bash
# where the board is plugged in (brings up the tunnel; prompts for your ssh password):
debug-bridge -port /dev/serial/by-id/usb-...-if00 -baud 115200 -tunnel <agent-host>

# on the agent host:
nc localhost 5555        # live, timestamped console with scrollback + reset markers
```

It wraps the system `ssh` (keys, `~/.ssh/config`, password prompt all work) and
warns clearly if the tunnel or the port has a problem. Build and full flag list
are in `tools/debug-bridge/README.md`.

Prefer a `/dev/serial/by-id/...` path for `-port` — it stays stable across
re-enumeration, whereas a bare `/dev/ttyACMx` can change number on reset. Read
the baud from the firmware's UART init (e.g. C-Sound is 115200 8N1).

This pairs naturally with a live GDB session: watch the boot/config prints stream
past while you step register settings, so firmware-reported state and on-target
register state are visible side by side.
