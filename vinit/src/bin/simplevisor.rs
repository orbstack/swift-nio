use std::{error::Error, process::Command};

use nix::{sys::signal::{kill, Signal}, unistd::Pid};
use signal_hook::iterator::Signals;

struct MonitoredChild {
    process: std::process::Child,
    args: Vec<String>,
}

// EXTREMELY simple process supervisor:
// - start processes
// - listen for all signals and forward them to children
//   * except on SIGUSR2: kill children, wait for exit, then restart
// - when any process exits, exit with the same exit code
//
// we keep tini around for signal forwarding
fn main() -> Result<(), Box<dyn Error>> {
    let mut children = std::env::args().into_iter()
        .skip(1)
        .map(|spec| {
            let child_args = spec.split(' ').map(|s| s.to_string()).collect::<Vec<_>>();
            let process = Command::new(&child_args[0])
                .args(&child_args[1..])
                .spawn()
                .unwrap();
            MonitoredChild {
                process,
                args: child_args,
            }
        })
        .collect::<Vec<_>>();

    // forward all signals to children
    let mut signals = Signals::new(&[
        signal_hook::consts::SIGTERM,
        signal_hook::consts::SIGINT,
        signal_hook::consts::SIGQUIT,
        signal_hook::consts::SIGABRT,
        signal_hook::consts::SIGUSR1,
        signal_hook::consts::SIGUSR2,
        signal_hook::consts::SIGCONT,
        signal_hook::consts::SIGTSTP,
        signal_hook::consts::SIGTTIN,
        signal_hook::consts::SIGTTOU,
        signal_hook::consts::SIGPIPE,
        signal_hook::consts::SIGALRM,
        signal_hook::consts::SIGCHLD,
        signal_hook::consts::SIGWINCH,
        signal_hook::consts::SIGIO,
        signal_hook::consts::SIGURG,
        signal_hook::consts::SIGSYS,
        signal_hook::consts::SIGXCPU,
        signal_hook::consts::SIGXFSZ,
        signal_hook::consts::SIGVTALRM,
        signal_hook::consts::SIGPROF,
    ])?;
    for signal in signals.forever() {
        println!(" [*] received {}", Signal::try_from(signal)?);
        match signal {
            signal_hook::consts::SIGCHLD => {
                // a child exited?
                for (i, child) in children.iter_mut().enumerate() {
                    if let Some(status) = child.process.try_wait()? {
                        // we exit as soon as a child does
                        // this covers cases of dockerd and k8s crashing
                        // and on shutdown, dockerd should exit first, so we exit faster. k8s would take a while
                        println!(" [*] service {} exited with {}", i, status);
                        std::process::exit(status.code().unwrap_or(1));
                    }
                }
            }
            signal_hook::consts::SIGUSR2 => {
                // kill children, wait for exit, then restart
                for (i, child) in children.iter_mut().enumerate() {
                    println!(" [*] restart service {}...", i);
                    // TODO: speed this up with SIGKILL?
                    // should be safe with Docker because we block requests and never start a new container, but still risky, and we kill container cgroups to speed that up anyway
                    // not sure if it's safe for k8s though
                    kill(Pid::from_raw(child.process.id() as i32), Some(Signal::SIGTERM))?;
                    let status = child.process.wait()?;
                    println!(" [*] restart service {}: exited with {}", i, status);
    
                    child.process = Command::new(&child.args[0])
                        .args(&child.args[1..])
                        .spawn()
                        .unwrap();
                }
            }
            _ => {
                // forward signal to children
                for child in children.iter_mut() {
                    kill(Pid::from_raw(child.process.id() as i32), Some(Signal::try_from(signal)?))?;
                }
            }
        }
    }

    Ok(())
}
