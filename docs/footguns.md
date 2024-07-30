# Footguns

## Unix

- `O_NONBLOCK` can be set on files, but it doesn't do anything.
- `SA_RESTART` signal handlers are not to be trusted; you can still get `EINTR` for many reasons.
- Don't retry `close` on EINTR; the fd may or may not have been closed. Yes, you might leak an fd, but if you retry it you might double-close another fd.
- Make sure everything is atomically CLOEXEC.
- `close` can block because that's when NFS flushes cached writes. You should check the error, but no one does because `defer` and `Drop` can't return errors.
- PIDs are racy, unless you're just reaping a direct child (because it'll be a zombie until you `wait` on it).
- Terminal modes are really complicated
- Writing to a broken pipe/socket causes SIGPIPE. Mask it; you'll still get EPIPE.
- Only async-signal-safe functions can be called from signal handlers.
- IPv6 sockets bound to `::` will get TCP46 connections (4-in-6 mapped IPv4 connections)

## Linux

- Some fds, such as `pidfd_open` and fds received by SCM_RIGHTS, have CLOEXEC set by default
- Linux has fully atomic CLOEXEC for all fd types; use it. `SOCK_CLOEXEC`, `pipe2`, `dup3`, etc.
- Covered mounts can't be unmounted, but only if they're in another user namespace.
  - Read-only mount locking works the same way. This means that privileged containers can remount read-only mounts as read-write.
- Don't open anything in a container's file system from the host. Symlinks could lead to container escapes.
  - If the container isn't running, it's safe to check for symlinks and then do it, but be very careful
  - If the container is running, symlink checks are racy. The container could swap them out after you check.
  - Use `openat2` with `RESOLVE_IN_ROOT` to avoid this in a non-racy way. This is slower, though, because you often have to open parent directories in order to act on files in them.
- pidfds can't be used to `waitpid` on non-direct-child processes to get status or reap them. You can only tell whether they're still alive, using `poll`.
- For race-free PID monitoring from another process, `clone` can atomically return a child's pidfd.
- Most namespaces can be set on a per-thread basis, but you need to make sure that all code in the same process uses `/proc/thread-self` and not `/proc/self` if you take advantage of this. `/proc/self/ns` is for the main thread.

## macOS

- It's not really possible to be fully cloexec-safe wrt. fork/exec races: there's no `SOCK_CLOEXEC` or `pipe2`, only `open(O_CLOEXEC)`.
  - For `dup`, use `fcntl(F_DUPFD_CLOEXEC)`
  - Go has a RW-lock (`syscall.ForkLock`) but other languages don't
  - Some FDs, such as `kqueue`, are CLOFORK.
- Use posix_spawn with `POSIX_SPAWN_CLOEXEC_DEFAULT` instead of fork/exec
  - This eliminates the problem, and even makes it OK to stop setting `O_CLOEXEC` at all, reducing `fcntl` calls for performance
  - In most languages, this means that you have to write your own posix_spawn FFI wrapper
- `getaddrinfo` opens a non-cloexec netctl fd, which gets inherited by child processes. Can't do anything about it.
- Non-portable APIs are usually better. Use them liberally. It doesn't make sense to be limited by the least common denominator of platforms.
- Sometimes there are BSD and Mach APIs that do the same thing. Prefer BSD because Mach is semi-private, but use Mach if it works better.
- `close` can return EINTR without closing the fd. Rust's `libc` crate links to `close$NOCANCEL` to fix this.
- pthread mutexes are slow. Use `os_unfair_lock` instead.
- macOS does have futexes: `os_sync_wait_on_address` on 14.4+, and the private `__ulock_wait2` before that.
- There's no public API for sendmmsg/recvmmsg, but there are private APIs: `sendmsg_x` and `recvmsg_x`.
- kqueue is fast when sharing the kqueue fd across threads, but there can still be lock contention on the knote buckets when registering/unregistering events.

## Go

### General

- Sending to a closed channel panics. Use contexts for cancellation.
- Non-existent map key = zero value
- Cgo auto-pins unsafe.Pointer arguments for the duration of the call
- There's no way to handle uncaught panics globally. The process will abort.
- Use `errors.Is(err, ...)`. Don't compare errors with `err ==`

### Syscalls

- Syscalls are slow due to runtime overhead, path copying, epoll_ctl registration attempts, preemption, and more
- `net.Conn.File()` and `net.FileConn` will dup the fd; `os.NewFile` does not
- `os.File` has a finalizer that closes the fd when garbage collected
  - Liveness is determined by last use within a function, **not whether it's in scope**
  - Use `runtime.KeepAlive` or `defer runtime.KeepAlive` to prevent GC until after the value of `.Fd()` is no longer being used
- `os.File.Fd()` unsets O_NONBLOCK on the fd
  - Use `util.GetFd` to get the fd without changing flags
- `os.NewFile` may call fstat and epoll_ctl to register it with the runtime poller
- An `os.File` can be closed after `.Fd()` is called but before fd is done being used. This is flagged by the race detector.
  - Use `util.UseFile`, which uses `syscall.RawConn.Control` and increments `f.pfd`'s refcount, to prevent this
    - This is expensive (allocates on each call, and increments/decrements an atomic refcount). Cache the `syscall.RawConn` for performance.
- Most syscalls call `runtime.entersyscall`, which has overhead. Use `unix.RawSyscall` to avoid this for fast non-blocking syscalls
  - ... but not on macOS, because you have to use libSystem wrappers

## Rust

### Syscalls

- Standard library does a redundant F_GETFL + F_SETFL(O_CLOEXEC) on every file open
- Tokio runtime is not to be trusted with signals (especially SIGCHLD), CLOEXEC safety, and forking
  - Write a posix_spawn wrapper if you need to spawn processes
- Use OwnedFd/BorrowedFd whenever possible
- Path syscalls are slow because they copy the Rust string to an on-stack buffer to add a null terminator. Use `CString`s in path-heavy code.

### Unsafe

- Aliasing rules: either many immutable refs + no mutable refs, or one mutable ref + no other refs
- Mutable refs to immutable data, and unaligned refs, are immediate UB even if never dereferenced or mutated
- Pointers are not subject to aliasing rules. Use `read`/`read_unaligned`/`read_volatile`
- Can't mutate without `&mut` (so no casting `&local` to `*mut _`; it must be `&mut local`)
- Bounds checks!

## Swift

### FFI

- Careful with async: it often tries to use the main thread, which isn't usable in vmgr because the main thread is Go
- Similarly, always create DispatchQueues; never use `DispatchQueue.main`
- The least error-prone APIs for managing FFI ownership and refcounts are:
  - `Unmanaged<T>.fromOpaque(ptr).takeUnretainedValue()`
  - `Unmanaged<T>.fromOpaque(ptr).takeRetainedValue()`
- `os_unfair_lock` isn't safe to use because it's a value type, and the address can change. Use `OSAllocatedUnfairLock`.

### Syscalls

- Apple frameworks use fds, XPC, and Mach ports internally. Don't assume they're CLOEXEC-safe.
  - NSTask seems to use posix_spawn, so it's not a huge problem
