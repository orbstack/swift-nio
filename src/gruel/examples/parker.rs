use gruel::{StartupAbortedError, StartupSignal, StartupTask};

pub struct Parker {
    pause_signal: StartupSignal,
    unpause_signal: StartupSignal,
}

impl Parker {
    pub fn pause(&self) -> Result<StartupTask, StartupAbortedError> {
        // Ensure that the unpause signal can't be completed immediately.
        let unpause_task = self.unpause_signal.resurrect_cloned();

        // Wait for everything to pause and, once that's done, give out an unpause task.
        self.pause_signal.wait().map(|_| unpause_task)
    }

    pub fn honor_pause(&self, pause_task: StartupTask) -> StartupTask {
        // Notify the pauser that we're done pausing.
        let pause_task = pause_task.success_keeping();

        // Wait for the unpause signal to be ready.
        let _ = self.unpause_signal.wait();

        // Resurrect the pause task.
        pause_task.resurrect()
    }
}

fn main() {}
