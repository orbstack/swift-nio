# The OrbStack Helper (vmgr) process

This is a very special process that contains

- Go code (vmgr)
- Rust code (libkrun)
- Swift code (GoVZF, aka. swext)

... all statically linked into a Go binary.

It also has vmnet, Hypervisor, and Virtualization entitlements.

## Global process state

- umask = `0` (for virtiofs server)
  - `chmod` your Unix sockets
- Only Go should install signal handlers. They have SA_RESTART set, but EINTR can still happen.
- There is no Mach exception port handler.
- SIGPIPE is not masked; Go handles it.
  - On other OSes, Go forwards SIGPIPE to the target thread if it's not a Go thread that caused it, but it doesn't forward SIGPIPE on macOS because the kernel doesn't deliver the signal to the thread that caused it.
  - libkrun sets SO_NOSIGPIPE on the virtio-net datagram socketpair
  - We don't explicitly use sockets in Swift, but vmnet and getaddrinfo use them internally
  - Maybe we should just mask SIGPIPE.
- We have a non-cloexec-safe netctl fd, created by libSystem getaddrinfo
- The main function is Go. If there's an uncaught Go panic, the process exits.
- There are 3 memory allocators: Go, libSystem malloc, and raw mach_vm_allocate for guest memory in libkrun
  - Address space races can happen. If you use mach_vm_allocate, always atomically reserve space upfront.
  - We don't currently do this, but Rust *can* use a different allocator, so make sure to use C malloc for owned FFI objects
- Not CLOEXEC safe. Don't spawn processs using fork+exec; always use posix_spawn with default cloexec (explicit inheritance)
  - We make an attempt to be cloexec-safe in vmgr (see [footguns](footguns.md)), but syscall.ForkLock usage isn't very consistent
  - The bigger problem is that synchronizing fork/cloexec RW-locks between Go, Rust, and Swift would be a nightmare
    - Swift and Rust don't even have such a lock, as far as I know
  - vmgr may spawn user-controlled processes, so this is a security issue!
    - CLI tools, shell/env setup, host SSH server, etc.
  - We really can't assume any kind of cloexec safety, because Apple libraries called from Swift (Security, FSEvents, etc.) use XPC internally, and could also be using file descriptors
- It's not safe to use Swift async on Go system threads. Everything must be `Task.detached`
- Swift: Do not use `DispatchQueue.main`. The main thread is Go. Everything goes on dedicated DispatchQueues.
- libkrun's vCPU threads have a special iopolicy to not materialize dataless files
- GoVZF has a bit of AppKit and other UI code (authorization prompts, notifications)
  - There's no NSApplicationMain so most of AppKit won't work, but some things are OK
- vmgr has an app bundle with an icon, bundle ID, signing ID, etc.
  - You must run the executable using a path that includes the bundle, e.g. `../out/OrbStack Helper.app/Contents/MacOS/OrbStack Helper`. Otherwise, macOS doesn't populate the correct icon/bundle ID.
  - We don't launch vmgr properly as app, like `open` would do. It's double-forked from the GUI app, or from the CLI tool, which means it can inherit TCC attribution context from other apps.
    - Need to fix this by making it a launchd agent with `AssociatedBundleIdentifiers`

**Everything can be security risk** because libkrun (VMM) runs in the same process.

## Why not move libkrun into another process?

Sharing an address space allows us to easily make use of shared memory, e.g. for network device ring buffers. This could be done across processes using XPC or Mach ports + shared memory, but it's a lot more complicated.

Still, this design has caused enough problems that moving libkrun out is probably a good idea.
