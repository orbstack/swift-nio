# starry

starry is a set of fast file system utilities.

As a library, it provides fast directory recursion and file metadata extraction helpers that make it easy to build fast tools.

Current utilities:

- [`cp`](src/commands/cp.rs)
- [`du`](src/commands/du.rs)
- [`find`](src/commands/find.rs)
- [`rm`](src/commands/rm.rs)
- [`tar`](src/commands/tar.rs)

See the respective files for more information.

## Why?

All starry utilities are safe against symlink races, which is important to prevent container escapes through code running in the root mount namespace. All operations are done on dirfds and/or with `O_NOFOLLOW`/`AT_SYMLINK_NOFOLLOW`, so the worst case is that the operation fails. starry never resolves more than path component at a time, removing the need for openat2(2).

Ideally, all utilities are best used in situations where the source is *not* modified concurrently, but this property ensures that a violation of this invariant will not lead to a security issue.

### `cp` and `tar`

There are no other utilities capable of preserving so many special file attributes, which is important for correctness when we're using these tools to migrate an entire machine rootfs. Specifically:

- Extended attributes (xattrs), even in the `trusted.*` namespace and on symlinks
- Inode flags (immutable, append-only)
- Sparse files
- Hard links
- Sockets
- FIFOs
- Char/block devices

### `rm`

starry removes immutable and append-only inode flags as necessary to allow deletion, instead of having to try once, then run `chattr -R`, then try again.

### Performance

Although many starry tools now primarily exist for correctness reasons, they're also designed to be as fast as possible: if we're writing these from scratch, why not make them fast?

All tools are designed to minimize syscalls and use the fastest possible syscalls. For example, we always use `openat(dirfd, filename)` rather than absolute paths, and we try to do clever tricks to get information out of failed syscalls (e.g. guess a regular file as the common case, then handle `EISDIR`).

When trade-offs are necessary, starry optimizes for common cases over obscure ones, and for btrfs (which is the filesystem used in production in OrbStack).

Another important optimization for `tar` is to do multi-threaded Zstd compression in-process, avoiding the memcpy, syscall, and wakeup overhead of pipes.
