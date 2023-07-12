use std::{error::Error, process::Command};

use nix::{sys::signal::{kill, Signal}, unistd::Pid};
use signal_hook::iterator::Signals;

// EXTREMELY simple process supervisor:
// - start a process
// - listen for all signals and forward them to the process
//   * except on SIGUSR2: kill child, wait for exit, then restart
// - when the process exits, exit with the same exit code
//
// we keep tini around for signal forwarding
fn main() -> Result<(), Box<dyn Error>> {
    let args = std::env::args().collect::<Vec<_>>();
    let mut child = Command::new(&args[1])
        .args(&args[2..])
        .spawn()?;

    // forward all signals to child
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
                // child exited?
                let status = child.try_wait()?;
                if let Some(status) = status {
                    println!(" [*] exited with {}", status);
                    std::process::exit(status.code().unwrap_or(1));
                }
            }
            signal_hook::consts::SIGUSR2 => {
                // kill child, wait for exit, then restart
                println!(" [*] restarting...");
                kill(Pid::from_raw(child.id() as i32), Some(Signal::SIGTERM))?;
                let status = child.wait()?;
                println!(" [*] restart: exited with {}", status);

                child = Command::new(&args[1])
                    .args(&args[2..])
                    .spawn()?;
            }
            _ => {
                // forward signal to child
                kill(Pid::from_raw(child.id() as i32), Some(signal.try_into()?))?;
            }
        }
    }

    Ok(())
}
