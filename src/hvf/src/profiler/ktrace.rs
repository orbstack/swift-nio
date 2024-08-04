use std::{
    collections::BTreeMap,
    io::{BufRead, BufReader},
    process::{Child, Command, Stdio},
    thread::JoinHandle,
};

use anyhow::anyhow;
use nix::{
    sys::signal::{kill, Signal},
    unistd::Pid,
};
use tracing::error;

use super::{
    thread::{ProfileeThread, ThreadId},
    time::MachAbsoluteTime,
    THREAD_NAME_TAG,
};

pub struct KtraceResults {
    pub threads: BTreeMap<ThreadId, KtraceThread>,
}

pub struct KtraceThread {
    // start time -> end time
    faults: BTreeMap<MachAbsoluteTime, MachAbsoluteTime>,
    last_fault_start: Option<MachAbsoluteTime>,
}

impl KtraceThread {
    pub fn is_time_in_fault(&self, time: MachAbsoluteTime) -> bool {
        // get highest fault time <= time
        // overlapping ranges are not possible, so only check one
        if let Some((start, end)) = self.faults.range(..=time).last() {
            // time is in a fault if it's between the start and end times
            time >= *start && time <= *end
        } else {
            // no faults, or all faults were before `time`
            false
        }
    }
}

pub struct Ktracer {
    child: Child,
    // use Option so we can take this out for Drop
    join_handle: Option<JoinHandle<anyhow::Result<KtraceResults>>>,
}

impl Ktracer {
    pub fn start(threads: &[ProfileeThread]) -> anyhow::Result<Self> {
        // sudo ktrace trace -t -f S0x0130 -p 50233 --csv
        let pid = std::process::id();
        let mut child = Command::new("sudo")
            // must use human-readable format, because CSV and JSON print relative nanoseconds since profiling start, and we don't know exactly when profiling started
            // -t: print timestamps as mach_absolute_time
            // -f S0x0130: class MACH, subclass MACH_VM
            // -N: don't resolve event names
            .args(["ktrace", "trace", "-t", "-f", "S0x0130", "-N"])
            .arg("-p")
            .arg(format!("{}", pid))
            .stdout(Stdio::piped())
            .spawn()?;

        // read output as a stream
        let stdout = child.stdout.take().unwrap();
        let mut reader = BufReader::new(stdout);

        let mut threads = threads
            .iter()
            .map(|t| {
                (
                    t.id,
                    KtraceThread {
                        faults: BTreeMap::new(),
                        last_fault_start: None,
                    },
                )
            })
            .collect::<BTreeMap<_, _>>();

        let handle = std::thread::Builder::new()
            .name(format!("{}: ktrace", THREAD_NAME_TAG))
            .spawn(move || -> anyhow::Result<KtraceResults> {
                // make sure format is right
                let mut line = String::new();
                let n = reader.read_line(&mut line)?;
                if n == 0 {
                    // EOF
                    return Err(anyhow!("ktrace output is empty"));
                }
                if line != "abstime                           delta(us)(duration)    debug-id                             arg1             arg2             arg3             arg4             thread-id        cpu  process-name(pid)                             \n" {
                    return Err(anyhow!("unexpected ktrace header: {:?}", line));
                }

                // parse the output
                loop {
                    // reuse buffer to avoid allocations
                    line.clear();
                    let n = reader.read_line(&mut line)?;
                    if n == 0 {
                        // EOF
                        break;
                    }

                    // remove \n
                    line.pop();

                    // there's another empty line after the header
                    if line.is_empty() {
                        continue;
                    }

                    // parse fields directly from iterator to avoid allocations
                    let mut fields = line.split_ascii_whitespace();

                    // [0]
                    let Some(mach_abs_time) = fields.nth(0) else {
                        continue;
                    };
                    let mach_abs_time = MachAbsoluteTime::from_raw(mach_abs_time.parse::<u64>()?);
                    // [2]
                    let Some(debug_id) = fields.nth(1) else {
                        continue;
                    };
                    let debug_id = u64::from_str_radix(debug_id, 16)?;
                    // [7]
                    let Some(tid) = fields.nth(4) else {
                        continue;
                    };
                    let tid = u64::from_str_radix(tid, 16)?;

                    // no need to check pid: TIDs are globally unique
                    let Some(thread) = threads.get_mut(&ThreadId(tid)) else {
                        // not a thread we're supposed to trace
                        continue;
                    };

                    // see /usr/share/misc/trace.codes
                    match debug_id {
                        // MACH_vmfault begin
                        0x1300009 => {
                            thread.last_fault_start = Some(mach_abs_time);
                        }

                        // MACH_vmfault end
                        0x130000a => {
                            if let Some(start) = thread.last_fault_start.take() {
                                thread.faults.insert(start, mach_abs_time);
                            }
                        }

                        _ => {}
                    }
                }

                Ok(KtraceResults { threads })
            })?;

        Ok(Self {
            child,
            join_handle: Some(handle),
        })
    }

    pub fn stop(mut self) -> anyhow::Result<KtraceResults> {
        // send a SIGINT and wait for it to drain
        kill(Pid::from_raw(self.child.id() as i32), Signal::SIGTERM)?;
        self.child.wait()?;

        let results = self
            .join_handle
            .take()
            .unwrap()
            .join()
            .map_err(|e| anyhow!("failed to join ktrace thread: {:?}", e))??;

        Ok(results)
    }
}

impl Drop for Ktracer {
    fn drop(&mut self) {
        if let Err(e) = self.child.kill() {
            error!("failed to kill ktrace: {:?}", e);
        }

        // need to reap to avoid zombie
        if let Err(e) = self.child.wait() {
            error!("failed to wait for ktrace: {:?}", e);
        }
    }
}
