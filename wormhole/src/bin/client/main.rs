use anyhow::anyhow;
use core::str;
use nix::sys::socket::{recv, send, MsgFlags};
use std::io::{prelude::*, stderr, stdin, stdout};
use std::net::TcpStream;
use std::os::unix::net::UnixStream;
use std::thread;
use std::{io::prelude::*, os::fd::AsRawFd};

use nix::unistd::{dup2, sleep};

fn main() -> anyhow::Result<()> {
    let rpc_server_socket = "/rpc_server.sock";

    let mut socket = match UnixStream::connect(rpc_server_socket) {
        Ok(sock) => sock,
        Err(err) => return Err(anyhow!("could not connect")),
    };

    let socket_fd = socket.as_raw_fd();

    let writer_handle = thread::spawn(move || -> anyhow::Result<()> {
        let mut stdin_lock = stdin().lock();
        let mut buffer = [0u8; 1024];

        loop {
            let n = stdin_lock.read(&mut buffer)?;
            if n == 0 {
                // eof from client
                break;
            }

            send(socket_fd, &buffer[..n], MsgFlags::empty())?;
        }
        Ok(())
    });

    let reader_handle = thread::spawn(move || -> anyhow::Result<()> {
        let mut stdout_lock = stdout().lock();
        let mut buf = [0u8; 1024];

        loop {
            let n = recv(socket_fd, &mut buf, MsgFlags::empty())?;
            if n == 0 {
                // eof from server
                break;
            }
            stdout_lock.write_all(&buf[..n])?;
            stdout_lock.flush()?;
        }

        Ok(())
    });

    writer_handle
        .join()
        .map_err(|_| anyhow!("Writer thread panicked"))?
        .map_err(|e| anyhow!("Writer thread error: {}", e))?;

    reader_handle
        .join()
        .map_err(|_| anyhow!("Reader thread panicked"))?
        .map_err(|e| anyhow!("Reader thread error: {}", e))?;

    Ok(())
}
