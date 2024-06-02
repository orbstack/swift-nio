use std::{
    error::Error,
    fs::{self, remove_file},
    os::unix::{fs::symlink, net::UnixDatagram, process::ExitStatusExt},
    process::Command,
    sync::{Arc, Mutex},
};

use nix::{
    sys::signal::{kill, Signal},
    unistd::Pid,
};
use serde::{Deserialize, Serialize};
use signal_hook::iterator::Signals;

const SERVICE_ID_DOCKER: usize = 0;
const SERVICE_ID_K8S: usize = 1;

struct MonitoredChild {
    process: std::process::Child,
    args: Vec<String>,
}

#[derive(Clone, Deserialize)]
struct SimplevisorConfig {
    init_commands: Vec<Vec<String>>,
    init_services: Vec<Vec<String>>,
    dep_services: Vec<Vec<String>>,
}

#[derive(Serialize)]
struct SimplevisorStatus {
    exit_statuses: Vec<i32>,
}

// EXTREMELY simple process supervisor:
// - start processes
// - listen for all signals and forward them to children
//   * except on SIGUSR2: kill children, wait for exit, then restart
// - when any process exits, exit with the same exit code
//
// we keep tini around for signal forwarding
fn main() -> Result<(), Box<dyn Error>> {
    // get config from env
    let config_str = std::env::var("SIMPLEVISOR_CONFIG")?;
    let config: SimplevisorConfig = serde_json::from_str(&config_str)?;

    let mut out_status = SimplevisorStatus {
        exit_statuses: vec![-1; config.init_services.len()],
    };

    // broken: EINVAL
    //init_system()?;

    // run init commands
    for init_command in config.init_commands.iter() {
        let status = Command::new(&init_command[0])
            .args(&init_command[1..])
            .status()?;
        if !status.success() {
            return Err(format!("init command {:?} failed with {}", init_command, status).into());
        }
    }

    // symlink: /var/run/docker.sock -> /var/run/docker.sock.raw
    // compat: https://docs.docker.com/desktop/extensions-sdk/guides/use-docker-socket-from-backend/
    symlink("/var/run/docker.sock", "/var/run/docker.sock.raw")?;

    let children = Arc::new(Mutex::new(vec![]));
    let children_clone = children.clone();
    let config_clone = config.clone();

    // start sd-notify first
    std::thread::spawn(move || {
        let children = children_clone;
        let config = config_clone;

        let _ = remove_file("/run/sd.sock");
        let listener = UnixDatagram::bind("/run/sd.sock").unwrap();
        let mut buf = [0; 256];
        loop {
            let (len, _) = listener.recv_from(&mut buf).unwrap();
            let msg = String::from_utf8_lossy(&buf[..len]).to_string();
            if msg == "READY=1" {
                println!(" [*] service 0 started");

                // now start dependent services
                for child_args in config.dep_services.iter() {
                    println!(" [*] starting dependent service");
                    let process = Command::new(&child_args[0])
                        .args(&child_args[1..])
                        .spawn()
                        .unwrap();

                    let child = MonitoredChild {
                        process,
                        args: child_args.clone(),
                    };

                    children.lock().unwrap().push(child);
                }
            }
        }
    });

    for child_args in config.init_services {
        let process = Command::new(&child_args[0])
            .args(&child_args[1..])
            .env("NOTIFY_SOCKET", "/run/sd.sock")
            .spawn()
            .unwrap();

        let child = MonitoredChild {
            process,
            args: child_args.clone(),
        };

        children.lock().unwrap().push(child);
    }

    let mut is_shutting_down = false;

    // remove config from env
    std::env::remove_var("SIMPLEVISOR_CONFIG");

    // forward all signals to children
    let mut signals = Signals::new([
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
        match signal {
            signal_hook::consts::SIGCHLD => {
                // a child exited?
                for (i, child) in children.lock().unwrap().iter_mut().enumerate() {
                    if let Some(status) = child.process.try_wait()? {
                        // we exit as soon as a child does
                        // this covers cases of dockerd and k8s crashing
                        // but on orderly shutdown (SIGTERM) we want to wait for dockerd. k8s shuts down faster
                        println!(" [*] service {} exited with {}", i, status);
                        let mut st_code = status.code().unwrap_or(1);
                        // check signal
                        if let Some(signal) = status.signal() {
                            // convert signal to exit code
                            st_code = 128 + signal;
                        }

                        // sometimes k8s exits with status 1 after SIGTERM. ignore it
                        if is_shutting_down && st_code == 1 && i == SERVICE_ID_K8S {
                            st_code = 0;
                        }

                        out_status.exit_statuses[i] = st_code;
                        if !is_shutting_down || i == SERVICE_ID_DOCKER {
                            // write out status
                            let out_status_str = serde_json::to_string(&out_status)?;
                            fs::create_dir_all("/.orb")?;
                            fs::write("/.orb/svstatus.json", out_status_str)?;

                            std::process::exit(st_code);
                        }
                    }
                }
            }

            signal_hook::consts::SIGUSR2 => {
                // kill children, wait for exit, then restart
                for (i, child) in children.lock().unwrap().iter_mut().enumerate() {
                    println!(" [*] restart service {}...", i);
                    // TODO: speed this up with SIGKILL?
                    // should be safe with Docker because we block requests and never start a new container, but still risky, and we kill container cgroups to speed that up anyway
                    // not sure if it's safe for k8s though
                    kill(
                        Pid::from_raw(child.process.id() as i32),
                        Some(Signal::SIGTERM),
                    )?;
                    let status = child.process.wait()?;
                    println!(" [*] restart service {}: exited with {}", i, status);

                    child.process = Command::new(&child.args[0])
                        .args(&child.args[1..])
                        .spawn()
                        .unwrap();
                }
            }

            _ => {
                println!(" [*] received {}", Signal::try_from(signal)?);

                // forward signal to children
                for child in children.lock().unwrap().iter() {
                    kill(
                        Pid::from_raw(child.process.id() as i32),
                        Some(Signal::try_from(signal)?),
                    )?;
                }

                if signal == signal_hook::consts::SIGTERM {
                    is_shutting_down = true;
                }
            }
        }
    }

    Ok(())
}
