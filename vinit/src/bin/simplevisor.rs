use std::{
    collections::HashMap,
    fs::File,
    fs::{self, remove_file},
    os::unix::{fs::symlink, net::UnixDatagram},
    process::Command,
    sync::{Arc, Mutex},
};

use anyhow::anyhow;
use nix::{
    mount::{mount, MsFlags},
    sys::{
        signal::{kill, Signal},
        wait::{waitid, Id, WaitPidFlag, WaitStatus},
    },
    unistd::Pid,
};
use serde::{Deserialize, Serialize};
use signal_hook::iterator::Signals;

type ServiceName = String;

const SERVICE_DOCKER: &str = "docker";
const SERVICE_K8S: &str = "k8s";

struct MonitoredChild {
    process: std::process::Child,
    args: Vec<String>,
}

#[derive(Clone, Deserialize)]
struct SimplevisorConfig {
    init_commands: Vec<Vec<String>>,
    init_services: HashMap<ServiceName, Vec<String>>,
    dep_services: HashMap<ServiceName, Vec<String>>,
    host_home: String,
}

#[derive(Serialize)]
struct SimplevisorStatus {
    exit_statuses: HashMap<ServiceName, i32>,
}

struct Supervisor {
    services: Arc<Mutex<HashMap<ServiceName, MonitoredChild>>>,
    config: SimplevisorConfig,
    shutting_down: bool,
    out_status: SimplevisorStatus,
}

impl Supervisor {
    fn start_sd_notify(&self) -> anyhow::Result<()> {
        let _ = remove_file("/run/sd.sock");
        let listener = UnixDatagram::bind("/run/sd.sock")?;

        let children = self.services.clone();
        let config = self.config.clone();

        std::thread::spawn(move || {
            let mut buf = [0; 256];
            loop {
                let (len, _) = listener.recv_from(&mut buf).unwrap();
                let msg = String::from_utf8_lossy(&buf[..len]).to_string();
                if msg == "READY=1" {
                    println!(" [*] service 0 started");

                    // now start dependent services
                    for (svc, child_args) in config.dep_services.iter() {
                        println!(" [*] starting dependent service");
                        let process = Command::new(&child_args[0])
                            .args(&child_args[1..])
                            .spawn()
                            .unwrap();

                        let child = MonitoredChild {
                            process,
                            args: child_args.clone(),
                        };

                        children.lock().unwrap().insert(svc.clone(), child);
                    }
                }
            }
        });

        Ok(())
    }

    fn spawn_init_services(&mut self) -> anyhow::Result<()> {
        // spawn init services, with sd-notify socket
        for (svc, child_args) in &self.config.init_services {
            let process = Command::new(&child_args[0])
                .args(&child_args[1..])
                .env("NOTIFY_SOCKET", "/run/sd.sock")
                .spawn()?;

            let child = MonitoredChild {
                process,
                args: child_args.clone(),
            };

            self.services.lock().unwrap().insert(svc.clone(), child);
        }

        Ok(())
    }

    fn monitor_loop(&mut self) -> anyhow::Result<()> {
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
                    self.reap_processes()?;
                }

                signal_hook::consts::SIGUSR2 => {
                    self.restart_children()?;
                }

                _ => {
                    println!(" [*] received {}", Signal::try_from(signal)?);

                    // forward signal to children
                    self.signal_children(signal)?;
                }
            }
        }

        Ok(())
    }

    fn reap_processes(&mut self) -> anyhow::Result<()> {
        loop {
            match waitid(Id::All, WaitPidFlag::WNOHANG | WaitPidFlag::WEXITED)? {
                WaitStatus::Exited(pid, status) => {
                    self.on_process_exit(pid, status)?;
                }
                WaitStatus::Signaled(pid, signal, _) => {
                    // bash returns 128 + signal
                    self.on_process_exit(pid, 128 + signal as i32)?;
                }
                WaitStatus::StillAlive => break,
                // don't care about other events
                _ => continue,
            }
        }

        Ok(())
    }

    fn on_process_exit(&mut self, pid: Pid, mut status: i32) -> anyhow::Result<()> {
        // a service exited?
        if let Some((name, _)) = self
            .services
            .lock()
            .unwrap()
            .iter()
            .find(|(_, child)| Pid::from_raw(child.process.id() as i32) == pid)
        {
            // we exit as soon as a child does
            // this covers cases of dockerd and k8s crashing
            // but on orderly shutdown (SIGTERM) we want to wait for dockerd. k8s shuts down faster
            println!(" [*] service {} exited with {}", name, status);

            // sometimes k8s exits with status 1 after SIGTERM. ignore it
            if self.shutting_down && status == 1 && name == SERVICE_K8S {
                status = 0;
            }

            self.out_status.exit_statuses.insert(name.clone(), status);
            if !self.shutting_down || name == SERVICE_DOCKER {
                // write out status
                let out_status_str = serde_json::to_string(&self.out_status)?;
                fs::create_dir_all("/.orb")?;
                fs::write("/.orb/svstatus.json", out_status_str)?;

                std::process::exit(status);
            }
        }

        // it wasn't a service, so probably a zombie. ignore
        Ok(())
    }

    fn restart_children(&mut self) -> anyhow::Result<()> {
        // kill children, wait for exit, then restart
        for (svc, child) in self.services.lock().unwrap().iter_mut() {
            println!(" [*] restart service {}...", svc);
            // TODO: speed this up with SIGKILL?
            // should be safe with Docker because we block requests and never start a new container, but still risky, and we kill container cgroups to speed that up anyway
            // not sure if it's safe for k8s though
            kill(
                Pid::from_raw(child.process.id() as i32),
                Some(Signal::SIGTERM),
            )?;
            let status = child.process.wait()?;
            println!(" [*] restart service {}: exited with {}", svc, status);

            child.process = Command::new(&child.args[0])
                .args(&child.args[1..])
                .spawn()?;
        }

        Ok(())
    }

    fn signal_children(&mut self, signal: i32) -> anyhow::Result<()> {
        for child in self.services.lock().unwrap().values() {
            kill(
                Pid::from_raw(child.process.id() as i32),
                Some(Signal::try_from(signal)?),
            )?;
        }

        if signal == signal_hook::consts::SIGTERM {
            self.shutting_down = true;
        }

        Ok(())
    }
}

fn init_system() -> anyhow::Result<()> {
    // create /init.scope cgroup to remove "(containerized)" from `docker system info`
    fs::create_dir_all("/sys/fs/cgroup/init.scope")?;

    // move into it
    let all_procs = fs::read_to_string("/sys/fs/cgroup/cgroup.procs")?;
    fs::write("/sys/fs/cgroup/init.scope/cgroup.procs", all_procs)?;

    // enable all controllers for sub-cgroups
    let controllers = fs::read_to_string("/sys/fs/cgroup/cgroup.controllers")?;
    fs::write(
        "/sys/fs/cgroup/cgroup.subtree_control",
        // prepend '+' to controller names
        controllers
            .split(' ')
            .map(|c| format!("+{}", c))
            .collect::<Vec<_>>()
            .join(" "),
    )?;

    Ok(())
}

// EXTREMELY simple process supervisor:
// - start processes
// - listen for all signals and forward them to children
//   * except on SIGUSR2: kill children, wait for exit, then restart
// - when any process exits, exit with the same exit code
//
// we keep tini around for signal forwarding
fn main() -> anyhow::Result<()> {
    // take config from env
    let config_str = std::env::var("SIMPLEVISOR_CONFIG")?;
    let config: SimplevisorConfig = serde_json::from_str(&config_str)?;
    std::env::remove_var("SIMPLEVISOR_CONFIG");

    init_system()?;

    // run init commands
    for init_command in config.init_commands.iter() {
        let status = Command::new(&init_command[0])
            .args(&init_command[1..])
            .status()?;
        if !status.success() {
            return Err(anyhow!(
                "init command {:?} failed with {}",
                init_command,
                status
            ));
        }
    }

    // symlink: /var/run/docker.sock -> /var/run/docker.sock.raw
    // compat: https://docs.docker.com/desktop/extensions-sdk/guides/use-docker-socket-from-backend/
    symlink("/var/run/docker.sock", "/var/run/docker.sock.raw")?;

    // LXC can't bind onto sockets (ENXIO due to open(dest) without O_PATH), so we bind docker.sock here to make "-v ~/.orbstack/run/docker.sock:/var/run/docker.sock" work
    // we don't strictly need the proxy here, but it helps for future path translation (and better matches what machines would get if not for the LXC bug)
    // this is optional in case the user's host fs is messed up
    let scon_docker_sock = "/opt/orbstack-guest/run/docker.sock";

    if let Err(e) = mount::<str, str, str, str>(
        Some(scon_docker_sock),
        &format!("{}/.orbstack/run/docker.sock", config.host_home),
        None,
        MsFlags::MS_BIND,
        None,
    ) {
        eprintln!(" [!] failed to bind docker.sock: {}", e);
    }

    File::create("/var/run/docker.sock")?;

    mount::<str, str, str, str>(
        Some(scon_docker_sock),
        "/var/run/docker.sock",
        None,
        MsFlags::MS_BIND,
        None,
    )?;

    let mut sv = Supervisor {
        services: Arc::new(Mutex::new(HashMap::new())),
        config,
        shutting_down: false,
        out_status: SimplevisorStatus {
            exit_statuses: HashMap::new(),
        },
    };

    // start sd-notify server first
    sv.start_sd_notify()?;

    sv.spawn_init_services()?;

    // agent waits on this to know that the boot was complete
    // otherwise, agent can race enabling tlsproxy with nftables creation
    fs::File::create("/run/.boot-complete")?;

    sv.monitor_loop()?;

    Ok(())
}
