// driver for wormhole-attach that opens the mount and gets container info

// ./driver <pid> <container env> ... --> runs wormhole-attach with the proper mount / fds

use std::{
    env, ffi::CString, fs::File, io, mem, os::fd::{AsRawFd, FromRawFd}, process::Command, thread
};

use libc::FD_CLOEXEC;
use nix::{fcntl::{fcntl, FcntlArg::{self, F_GETFD}, FdFlag}, sys::wait::waitpid, unistd::{execve, execvp, fork, pipe, read, ForkResult}};
use serde::{Deserialize, Serialize};
use wormhole::newmount::open_tree;
use std::os::unix::process::CommandExt;

#[derive(Serialize, Deserialize, Debug, Clone)]
pub struct WormholeConfig {
    pub init_pid: i32,
    #[serde(default)]
    pub wormhole_mount_tree_fd: i32,
    #[serde(default)]
    pub exit_code_pipe_write_fd: i32,
    #[serde(default)]
    pub log_fd: i32,
    #[serde(default)]
    pub drm_token: String,

    pub container_env: Option<Vec<String>>,
    pub container_workdir: Option<String>,

    pub entry_shell_cmd: Option<String>,
}

fn main() -> anyhow::Result<()> {
    let config_str = std::env::args().nth(1).unwrap();
    let mut config = serde_json::from_str::<WormholeConfig>(&config_str)?;

    // see `doWormhole` in scon/ssh.go (~L300)
    let wormhole_mount = open_tree("/mnt/wormhole-unified/nix", 0x80000 | 0x1 | 0x800)?;
    let (exit_code_pipe_read_fd, exit_code_pipe_write_fd) = pipe()?;
    let (log_pipe_read_fd, log_pipe_write_fd) = pipe()?;
    let wormhole_mount_fd = wormhole_mount.as_raw_fd();


    // disable cloexec for fd that we pass to wormhole-attach
    fcntl(
        wormhole_mount_fd,
        FcntlArg::F_SETFD(
            FdFlag::from_bits_truncate(fcntl(wormhole_mount_fd, F_GETFD)?) & !FdFlag::FD_CLOEXEC
        ),
    )?;
    fcntl(
        exit_code_pipe_write_fd,
        FcntlArg::F_SETFD(
            FdFlag::from_bits_truncate(fcntl(wormhole_mount_fd, F_GETFD)?) & !FdFlag::FD_CLOEXEC
        ),
    )?;
    fcntl(
        log_pipe_write_fd,
        FcntlArg::F_SETFD(
            FdFlag::from_bits_truncate(fcntl(wormhole_mount_fd, F_GETFD)?) & !FdFlag::FD_CLOEXEC
        ),
    )?;

    config.wormhole_mount_tree_fd = wormhole_mount_fd.as_raw_fd();
    config.exit_code_pipe_write_fd = exit_code_pipe_write_fd;
    config.drm_token = String::from("eyJhbGciOiJFZERTQSIsImtpZCI6IjEiLCJ0eXAiOiJKV1QifQ.eyJzdWIiOiIiLCJlbnQiOjEsImV0cCI6MiwiZW1nIjpudWxsLCJlc3QiOm51bGwsImF1ZCI6Im1hY3ZpcnQiLCJ2ZXIiOnsiY29kZSI6MTA3MDEwMCwiZ2l0IjoiMmUzZjdlZWVhNjQ0NWEyZjZlYWI1MzM0MTkzNjBkZmU2NmZiODNkYSJ9LCJkaWQiOiI3YmE5ZjA1ZDBlMGY2NTI3MjVkYzA3NjM5Y2VmYTg2NTM2ZWVlMmU5NTc4NDk2OWVlODcwZWMyZDY2YjEzMDI0IiwiaWlkIjoiYzdlYzY1M2FmZDljMDIxNjZlZjY2Nzc2MGVkYWNmODA0ZDc4OTlhZDE3YmQ1YWIxYzU4YzE4OGVjOGYxZTExYiIsImNpZCI6ImU1NjZiZjRiNmExNjNjYTM1NGU2OGQzYmU2ZjAzZDlmNzFkMzYxZTdhMmIxNjMzZDcwMzE0MmE2ODIwNmNjNDciLCJpc3MiOiJkcm1zZXJ2ZXIiLCJpYXQiOjE3MjY2ODQyMjUsImV4cCI6MTcyNzI4OTAyNSwibmJmIjoxNzI2Njg0MjI1LCJkdnIiOjEsIndhciI6MTcyNjk3MTM3MiwibHhwIjoxNzI3NTc2MTcyfQ.asnYZORqAuIxyuusi8GVLql6GzF3oSEyyTJnQDw2F4FE11mRAJGWWm6wVWaphnyQUYptTmDvbp3VeRBg0HWGAw");


    config.log_fd = log_pipe_write_fd;

    let serialized = serde_json::to_string(&config)?;
    println!("wormhole config: {}", serialized);
    match unsafe { fork()? } {
        ForkResult::Child => {
            // Prepare the command and arguments

            // Execute the command
            execvp(&CString::new("./wormhole-attach")?, &[
                CString::new("./wormhole-attach")?,
                CString::new(serialized)?,
            ])?;
            std::process::exit(0);
        }
        ForkResult::Parent { child } => {
            waitpid(child, None)?;
            let mut buffer = [0u8; mem::size_of::<i32>()]; // Buffer to hold 4 bytes (i32)
            read(exit_code_pipe_read_fd, &mut buffer)?;
            let num = i32::from_ne_bytes(buffer);
            println!("debug: wormhole finished with exit code {}", num)
        }
    } 


    // println!("Command stdout:\n{}", String::from_utf8_lossy(&output.stdout));
    // println!("Command stderr:\n{}", String::from_utf8_lossy(&output.stderr));
    // println!("Result: {}", output.status);

    Ok(())
}

/*
locally:
kevin@orbstack macvirt % docker build --ssh default -t localhost:5000/wormhole-rootfs -f wormhole/remote/Dockerfile .
kevin@orbstack macvirt % docker push localhost:5000/wormhole-rootfs


on remote:
kevin@testremote:~$ docker pull 198.19.249.3:5000/wormhole-rootfs
kevin@testremote:~$ docker run -d --privileged --pid host --net host --cgroupns host -v wormhole-data:/data 198.19.249.3:5000/wormhole-rootfs

*/
