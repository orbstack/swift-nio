# macOS performance/debugging tools

(roughly ordered by how often I use them)

## General tips

- Debug builds of vmgr have Go + Rust + Swift symbols; release builds have none.
  - `release-with-debug` is the default libkrun profile. It has symbols.

## Activity Monitor

GUI for single-task spindump, idle wakeups, memory phys_footprint and RSS, syscall + context switch + page fault counts, and open FD info (paths, pipes, socket addresses). More powerful than it appears.

Tips:

- Set **View > Update Frequency > Very often (1 sec)** to make it usable
- CPU tab:
  - Enable "Energy Impact" column for estimated power usage
  - Enable "Idle Wake Ups" column: wakeups are a major source of battery drain
  - Enable "% GPU" column to find non-CPU sources of battery drain
- Memory tab:
  - Check memory pressure
  - "Memory" = task_ledgers.phys_footprint. Enable "Real Mem" column for RSS
- Double click process for details
  - "Sample" is a quick single-process spindump (low sampling rate). Userspace stacks only.
  - "Open Files and Ports" tab = open FDs
  - **If you ever see a hang (even if it's short), quickly open Activity Monitor, search for "orb", double click it, and click Sample** to grab stack traces for later debugging. I keep it in my Dock to make this as fast as possible.
- To see P-core and E-core utilization: View > CPU History
- Don't leave it open in the background for too long if you care about battery or performance. `sysmond` uses ~80% CPU.

## spindump

CLI sampling profiler. Includes unified kernel/userspace stacks. Can profile a single process or the whole system (including kernel threads). Very useful for debugging hangs/lockups too, not only for optimizing. Low overhead.

Install a [KDK](https://developer.apple.com/download/all/) to get kernel symbols. The KDK must match your macOS version. Not all versions have KDKs.

Usage:

- System-wide: `sudo spindump`
- System-wide, but show target process first: `sudo spindump 'OrbStack Helper'`
- Single process: `sudo spindump -onlytarget 'OrbStack Helper'`
- Single process, 10 sec, 2000 Hz sampling (500 usec): `sudo spindump -onlytarget 'OrbStack Helper' 10 500u`
- System-wide, with kernel symbols: `sudo spindump -dsym /Volumes/Macintosh\ HD/Library/Developer/KDKs/KDK_14.5_23F79.kdk/System/Library/Kernels/kernel.release.t6031.dSYM -onlytarget 'OrbStack Helper' 10 500u`
  - `t6031` is M3 Pro/Max. Find your chip's kernel in `uname -a`.

If you're in a rush or forget to add `-dsym`, spindump can re-process output from an earlier run and resolve kernel symbols:

```bash
sudo spindump -dsym /Volumes/Macintosh\ HD/Library/Developer/KDKs/KDK_14.5_23F79.kdk/System/Library/Kernels/kernel.release.t6031.dSYM -i /tmp/OrbStack_Helper_79554.3.spindump.txt
```

## flamegraph-rs

CLI sampling profiler. Userspace stacks + single process only. Visualized as a flamegraph, which I prefer for optimization. Low overhead.

Usage: `sudo flamegraph -p $(pidof 'OrbStack Helper')`

This uses DTrace but works with SIP as long as you run it as root, and the target process has the `com.apple.security.get-task-allow` entitlement (which debug vmgr builds do).

## log stream

Read unified system logs. Includes kernel logs, mDNSResponder, code signing, and many other system components.

Usage: `sudo log stream`

Console.app is a GUI for this, but I rarely use it.

## footprint

Read user-facing memory usage accounting. This reports phys_footprint, which is what Activity Monitor shows as "Memory", but on a per-region basis.

Usage: `sudo footprint 'OrbStack Helper'`

Supports multiple processes.

## vmmap

Read detailed memory region info and accounting. Includes virtual/dirty/resident/swapped, but not phys_footprint.

Usage: `sudo vmmap 'OrbStack Helper'`

## /Library/Logs/DiagnosticReports

System-wide process crash dumps and kernel panic dumps.

## ~/Library/Logs/DiagnosticReports

User-specific process crash dumps.

## tcpdump

Network packet dumper. Useful for debugging network issues, especially on bridge interfaces.

Usage: `sudo tcpdump -ni bridge100`

Wireshark also works, but I usually save pcaps using tcpdump and then open them in Wireshark later.

## Instruments

GUI profiler. Can read CPU performance counters.

Comes with Xcode: open Xcode, then go to Xcode menu > Open Developer Tool > Instruments.

- System Trace = system-wide spindump + VM faults
- Time Profiler = single-process spindump
- CPU Profiler = single-process PMU-based profiling

I usually use Time Profiler (for IO-bound workloads) or CPU Profiler (for compute-bound workloads).

Select "OrbStack Helper" in the top-left, under System Processes. No need to re-select after restarting vmgr; it searches for the process name automatically if the PID is gone.

## fs_usage

System-wide tracer for I/O calls, including files, sockets, fcntl, recvmsg, ioctl, stat, etc. `grep` to avoid recursion (terminal's read/write calls causing infinite output).

Usage: `sudo fs_usage | grep -i orb`

## ktrace

System-wide or per-process tracer for syscalls and much more. This is the closest thing to `strace` on macOS. Works with SIP enabled.

Usage: `sudo ktrace trace -Ss -f C4,S0x010c,C2,C3 -n -p $(pidof 'OrbStack Helper')`

Filters:

- `C4` = BSD syscalls. Arguments not included.
- `S0x010c` = Mach syscalls. Arguments not included.
  - High byte: class `0x01` = `DBG_MACH`
  - Low byte: subclass `0x0c` = `DBG_MACH_EXCP_SC`
- `C2` = Network
- `C3` = VFS. Includes paths.
- [List of all filters](https://github.com/apple-oss-distributions/xnu/blob/94d3b452840153a99b38a3a9659680b2a006908e/bsd/sys/kdebug.h)

Tips:

- Hypervisor calls are `MSC_kern_invalid_5`

## eslogger

System-wide tracer for many syscalls, using the Endpoint Security framework.

Usage:

- Trace everything: `sudo eslogger $(eslogger --list-events) | jq`
  - This is too much output to read
- Parse JSON into a simpler format: `sudo eslogger $(eslogger --list-events) | $MACVIRT/exp/mac/parse_es.py`
- List events: `eslogger --list-events`
  - Then pass specific events to `eslogger`

This is focused on what endpoint security sofware would want to trace, so it doesn't include all syscalls. I use a combination of spindump, fs_usage, and eslogger to plug the gaps.

## lsof

List open FDs and info, including pipe pairs, kqueues, file paths, socket addresses, and more. Useful for finding leaks or unexpected FDs.

Usage: `sudo lsof -np $(pidof 'OrbStack Helper')`

## lsmp

List open Mach ports.

## sysctl

Many useful stats, counters, and debug knobs are exposed via sysctl.

For example, to debug madvise(MADV_FREE_REUSABLE):

```
❯ sysctl -a | grep reusable
vm.page_reusable_count: 101113
vm.reusable_success: 1391109786
vm.reusable_failure: 6058822
```

Or NFS client logs: `sysctl vfs.generic.nfs.client.debug_ctl=2147483647`

## lldb

CLI debugger. Use this instead of `gdb`.

Works with SIP if target process has the `com.apple.security.get-task-allow` entitlement (which debug vmgr builds do).

- `bt`: backtrace

## powermetrics

System-wide power+energy usage, component power usage (CPU, GPU, etc.), CPU/GPU frequencies, IRQ rate, disk/network activity. Use this if you're working on battery efficiency.

Usage: `sudo powermetrics`

Look for power readings:

```
CPU Power: 3746 mW
GPU Power: 59 mW
ANE Power: 0 mW
Combined Power (CPU + GPU + ANE): 3804 mW
```

## macvirt: exp/power/powermon2.c

Measure energy usage of an entire process group (coalition). Used for public benchmarks.

`powermon.c` uses `libpmenergy` instead of coalition, which returns the same values for single-process measurements on M1.

## vm_stat

System-wide virtual memory stats. Can also sample at an interval.

Usage:

- One-shot: `sudo vm_stat`
- Sample every second: `sudo vm_stat 1`

## memory_pressure

System-wide memory pressure stats.

## nfsstat

NFS client debug info.

Usage: `nfsstat -m` to list mounts and their flags

## taskpolicy

Change process QoS tiers, background priority, and I/O policy.

## SIP

We can't use these because SIP needs to stay on for enterprise security requirements, but it might be worth knowing about them:

- `dtruss`: DTrace script similar to Linux strace, except it's missing most syscalls
  - Last time I tried, this causes a kernel panic if you run it after the computer has been through at least one sleep/wake cycle, so reboot before running it.
- `procsystime`: DTrace script to measure syscall times
  - I've never tried this one
