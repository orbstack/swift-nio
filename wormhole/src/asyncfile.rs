use nix::fcntl::{fcntl, FcntlArg, OFlag};
use nix::unistd::dup;
use std::fs::File;
use std::io::{Read, Write};
use std::os::fd::AsRawFd;
use std::os::unix::io::{FromRawFd, RawFd};
use std::pin::Pin;
use std::task::{ready, Context, Poll};
use tokio::io::unix::{AsyncFd, TryIoError};
use tokio::io::{AsyncRead, AsyncWrite, ReadBuf};
use tracing::trace;

fn set_nonblocking(fd: RawFd) -> nix::Result<()> {
    let flags = fcntl(fd, FcntlArg::F_GETFL)?;
    let new_flags = OFlag::from_bits_truncate(flags) | OFlag::O_NONBLOCK;
    fcntl(fd, FcntlArg::F_SETFL(new_flags))?;
    Ok(())
}

pub struct AsyncFile {
    inner: AsyncFd<File>,
}

impl AsyncFile {
    pub fn new(fd: RawFd) -> std::io::Result<Self> {
        // Set the file descriptor to non-blocking mode
        set_nonblocking(fd)?;

        // Wrap the file descriptor in a `File`
        let file = unsafe { File::from_raw_fd(fd) };

        Ok(Self {
            inner: AsyncFd::new(file)?,
        })
    }

    pub fn from(file: File) -> std::io::Result<Self> {
        set_nonblocking(file.as_raw_fd())?;
        Ok(Self {
            inner: AsyncFd::new(file)?,
        })
    }

    pub fn as_raw_fd(&mut self) -> i32 {
        self.inner.as_raw_fd()
    }

    pub fn try_clone(&self) -> std::io::Result<Self> {
        let fd = self.inner.get_ref().as_raw_fd();
        let fd_dup = dup(fd)?;
        Self::new(fd_dup)
    }
}

impl AsyncRead for AsyncFile {
    fn poll_read(
        self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        buf: &mut ReadBuf<'_>,
    ) -> Poll<std::io::Result<()>> {
        loop {
            trace!("looping poll read");
            let mut guard = ready!(self.inner.poll_read_ready(cx))?;
            trace!("got guard");

            match guard.try_io(|inner| inner.get_ref().read(buf.initialize_unfilled())) {
                Ok(Ok(n)) => {
                    trace!("got n data");
                    buf.advance(n);
                    return Poll::Ready(Ok(()));
                }
                Ok(Err(ref e)) if e.kind() == std::io::ErrorKind::WouldBlock => {
                    // Continue the loop to wait for readiness
                    trace!("wouldblock");
                    // guard.clear_ready();
                    // return Poll::Pending;
                    continue;
                }
                Ok(Err(e)) => {
                    trace!("error {:?}", e);
                    return Poll::Ready(Err(e));
                }
                Err(e) => {
                    // Readiness changed, need to poll again
                    trace!("issue {:?}", e);
                    continue;
                }
            }
        }
    }
}

impl AsyncWrite for AsyncFile {
    fn poll_write(
        self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        buf: &[u8],
    ) -> Poll<std::io::Result<usize>> {
        loop {
            let mut guard = ready!(self.inner.poll_write_ready(cx))?;

            match guard.try_io(|inner| inner.get_ref().write(buf)) {
                Ok(Ok(n)) => {
                    return Poll::Ready(Ok(n));
                }
                Ok(Err(ref e)) if e.kind() == std::io::ErrorKind::WouldBlock => {
                    guard.clear_ready();
                    return Poll::Pending;
                }
                Ok(Err(e)) => {
                    return Poll::Ready(Err(e));
                }
                Err(_would_block) => {
                    // Readiness changed, need to poll again
                    continue;
                }
            }
        }
    }

    fn poll_flush(self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<std::io::Result<()>> {
        // For files, flush may be a no-op, but implement if necessary
        Poll::Ready(Ok(()))
    }

    fn poll_shutdown(self: Pin<&mut Self>, _cx: &mut Context<'_>) -> Poll<std::io::Result<()>> {
        // Implement shutdown logic if needed
        Poll::Ready(Ok(()))
    }
}
