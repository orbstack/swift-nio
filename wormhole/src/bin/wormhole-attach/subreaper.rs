use std::{thread::sleep, time::Duration};

use nix::{
    errno::Errno,
    sys::wait::{waitpid, WaitPidFlag, WaitStatus},
    unistd::Pid,
};

fn reap_last_zombies() -> anyhow::Result<()> {
    // wait up to 25 ms for zombies to exit
    sleep(Duration::from_millis(25));

    // reap all remaining zombies
    loop {
        match waitpid(None, Some(WaitPidFlag::WNOHANG)) {
            Ok(WaitStatus::StillAlive) => return Ok(()),
            Ok(_) => {}
            Err(Errno::ECHILD) => return Ok(()),
            Err(e) => return Err(e.into()),
        }
    }
}

pub fn run(payload_pid: Pid) -> anyhow::Result<()> {
    // keep waiting for processes
    loop {
        let res = waitpid(None, None)?;
        match res {
            // exit/return if exited/signaled process is the payload (to signal grandparent waiter)
            WaitStatus::Exited(pid, _) if pid == payload_pid => break,
            WaitStatus::Signaled(pid, _, _) if pid == payload_pid => break,
            // do nothing for zombie processes - just reap them
            _ => {}
        }
    }

    // to reap grandchildren, don't exit immediately
    // but also don't kill them -- allow bg services to keep running
    reap_last_zombies()?;
    Ok(())
}
