use nix::fcntl::{fcntl, FcntlArg};
use std::fs::File;
use std::io::{Read, Write};
use std::os::fd::{AsRawFd, OwnedFd};
use std::os::unix::io::{FromRawFd, RawFd};
use std::pin::Pin;
use std::task::{ready, Context, Poll};
use tokio::io::unix::AsyncFd;
use tokio::io::{AsyncRead, AsyncWrite, ReadBuf};

pub struct AsyncFile {
    inner: AsyncFd<File>,
}

impl AsyncFile {
    // AsyncFile assumes that the resource is set to nonblocking mode; it
    // is the caller's responsibility to ensure O_NONBLOCK on the
    // underlying file descriptor
    pub fn new(fd: RawFd) -> std::io::Result<Self> {
        let file = unsafe { File::from_raw_fd(fd) };
        Ok(Self {
            inner: AsyncFd::new(file)?,
        })
    }

    // note: does not set O_NONBLOCK, see above
    pub fn from(file: File) -> std::io::Result<Self> {
        Ok(Self {
            inner: AsyncFd::new(file)?,
        })
    }

    pub fn as_raw_fd(&mut self) -> i32 {
        self.inner.as_raw_fd()
    }

    pub fn try_clone(&self) -> std::io::Result<Self> {
        let fd = self.inner.get_ref().as_raw_fd();
        let fd_dup = fcntl(fd, FcntlArg::F_DUPFD_CLOEXEC(3))?;
        Self::new(fd_dup)
    }
}

impl From<AsyncFile> for OwnedFd {
    fn from(file: AsyncFile) -> OwnedFd {
        OwnedFd::from(file.inner.into_inner())
    }
}

impl AsyncRead for AsyncFile {
    fn poll_read(
        self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        buf: &mut ReadBuf<'_>,
    ) -> Poll<std::io::Result<()>> {
        loop {
            let mut guard = ready!(self.inner.poll_read_ready(cx))?;

            match guard.try_io(|inner| inner.get_ref().read(buf.initialize_unfilled())) {
                Ok(Ok(n)) => {
                    buf.advance(n);
                    return Poll::Ready(Ok(()));
                }
                Ok(Err(ref e)) if e.kind() == std::io::ErrorKind::WouldBlock => {
                    continue;
                }
                Ok(Err(e)) => {
                    return Poll::Ready(Err(e));
                }
                Err(_would_block) => {
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
                    continue;
                }
                Ok(Err(e)) => {
                    return Poll::Ready(Err(e));
                }
                Err(_would_block) => {
                    continue;
                }
            }
        }
    }

    fn poll_flush(self: Pin<&mut Self>, _cx: &mut Context<'_>) -> Poll<std::io::Result<()>> {
        Poll::Ready(Ok(()))
    }

    fn poll_shutdown(self: Pin<&mut Self>, _cx: &mut Context<'_>) -> Poll<std::io::Result<()>> {
        Poll::Ready(Ok(()))
    }
}
