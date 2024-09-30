use anyhow::anyhow;
use core::str;
use libc::{sendmsg, tls12_crypto_info_chacha20_poly1305};
use nix::sys::socket::{recv, send, MsgFlags};
use nix::sys::termios::{cfmakeraw, tcgetattr, tcsetattr, InputFlags, LocalFlags, SetArg};
use serde::{Deserialize, Serialize};
use std::io::{prelude::*, stderr, stdin, stdout};
use std::net::TcpStream;
use std::os::unix::net::UnixStream;
use std::thread;
use std::{io::prelude::*, os::fd::AsRawFd};
use wormhole::TermiosParams;

use nix::libc::{cc_t, tcflag_t, NCCS};
use nix::unistd::{dup2, sleep};

struct Cleanup;

impl Drop for Cleanup {
    fn drop(&mut self) {
        // todo
    }
}

fn main() -> anyhow::Result<()> {
    let STDIN_FD = stdin();
    // let mut termios = tcgetattr(&STDIN_FD)?;
    // cfmakeraw(&mut termios);
    // termios.local_flags.remove(LocalFlags::ICANON);
    // termios.local_flags.remove(LocalFlags::ECHO);
    // termios.local_flags.remove(LocalFlags::ECHONL);
    // tcsetattr(&STDIN_FD, SetArg::TCSAFLUSH, &termios)?;

    // println!("termios {:?}", termios.local_flags);
    // let host_termios = std::env::args().nth(1).unwrap();
    // let mut host_termios_params = serde_json::from_str::<TermiosParams>(&host_termios)?;

    // println!("host termios settings {:?}", host_termios_params);

    let rpc_server_socket = "/rpc_server.sock";

    let mut socket = match UnixStream::connect(rpc_server_socket) {
        Ok(sock) => sock,
        Err(err) => return Err(anyhow!("could not connect to rpc sock")),
    };

    let socket_fd = socket.as_raw_fd();

    // let termios_payload = bincode::serialize(&host_termios_params)?;
    // let len_bytes = u32::try_from(termios_payload.len())?.to_be_bytes();
    // send(socket_fd, &len_bytes, MsgFlags::empty())?;
    // send(socket_fd, &termios_payload, MsgFlags::empty())?;

    let to_socket = thread::spawn(move || -> anyhow::Result<()> {
        let mut stdin_lock = stdin().lock();
        let mut buf = [0u8; 1024];

        loop {
            let n = stdin_lock.read(&mut buf)?;
            if n == 0 {
                // eof from client
                break;
            }

            send(socket_fd, &buf[..n], MsgFlags::empty())?;
        }
        Ok(())
    });

    let from_socket = thread::spawn(move || -> anyhow::Result<()> {
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

    let _ = to_socket.join();
    let _ = from_socket.join();

    Ok(())
}
