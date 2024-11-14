#![allow(clippy::missing_safety_doc)]

use std::{io, mem, ptr, sync::Mutex};

// === `multiplexer.h` bindings === //

pub const FFI_INCLUDE_DIR: &str = env!("FFI_INCLUDE_DIR");

#[derive(Debug, Copy, Clone, Eq, PartialEq)]
#[repr(C)]
pub enum SignalVerdict {
    Continue,
    Handle,
    ForceDefault,
}

pub type SignalCallback = unsafe extern "C" fn(
    signum: i32,
    info: *mut libc::siginfo_t,
    uap: *mut libc::c_void,
    userdata: *mut libc::c_void,
) -> SignalVerdict;

// === `register_handler` == //

extern "C" {
    fn orb_init_signal_multiplexer(signum: i32, sigaction: libc::sigaction) -> libc::boolean_t;

    fn orb_push_signal_multiplexer(
        signum: i32,
        user_action: SignalCallback,
        userdata: *mut libc::c_void,
    ) -> libc::boolean_t;

    fn orb_signal_multiplexer(signum: i32, info: *mut libc::siginfo_t, uap: *mut libc::c_void);
}

struct RegistryState {
    initialized_set: Vec<i32>,
}

static REGISTRY_STATE: Mutex<RegistryState> = Mutex::new(RegistryState {
    initialized_set: Vec::new(),
});

pub unsafe fn register_handler(
    signum: i32,
    user_action: SignalCallback,
    userdata: *mut libc::c_void,
) -> anyhow::Result<()> {
    // Holding this lock also ensures that we don't modify the multiplex list concurrently since doing
    // so would cause handlers to potentially be skipped.
    let mut registry = REGISTRY_STATE.lock().unwrap();

    // Initialize the signal handler if necessary.

    // `O(n)` set implementation because I can't be bothered to bring in `hashbrown` and `rustc_hash`
    // to const-initialize a `HashSet` correctly. The set of `signum`s is very much bounded.
    if !registry.initialized_set.contains(&signum) {
        // Make sure `sigaltstack` was set up by either Go or Rust
        let mut stack: libc::stack_t = unsafe { mem::zeroed() };
        let ret = unsafe { libc::sigaltstack(ptr::null(), &mut stack) };
        if ret == -1 {
            return Err(anyhow::anyhow!(
                "failed to create `sigaltstack`: {}",
                io::Error::last_os_error(),
            ));
        }

        if stack.ss_flags & libc::SS_DISABLE != 0 {
            return Err(anyhow::anyhow!("no sigaltstack"));
        }

        // fetch old signal handler first
        let mut old_action: libc::sigaction = unsafe { mem::zeroed() };
        let ret = unsafe { libc::sigaction(signum, ptr::null(), &mut old_action) };
        if ret == -1 {
            return Err(anyhow::anyhow!(
                "failed to fetch old `sigaction`: {}",
                io::Error::last_os_error(),
            ));
        }

        // we can only forward to old handlers that use signal stack
        if !matches!(old_action.sa_sigaction, libc::SIG_DFL | libc::SIG_IGN)
            && old_action.sa_flags & libc::SA_ONSTACK == 0
        {
            return Err(anyhow::anyhow!("old handler doesn't use signal stack"));
        }

        // Save old handler first to prevent race if new handler fires immediately.
        if unsafe { orb_init_signal_multiplexer(signum, old_action) } == 0 {
            return Err(anyhow::anyhow!(
                "failed to initialize signal multiplexer (oom?)"
            ));
        }

        // install new signal handler
        let new_action = libc::sigaction {
            sa_sigaction: orb_signal_multiplexer as usize,
            // Go requires SA_ONSTACK
            // SA_RESTART makes little sense for SIGBUS, but doesn't hurt to have
            // no SA_NODEFER: SIGBUS in the SIGBUS handler is definitely bad, so just crash
            // can't use signal_hook: it doesn't set SA_ONSTACK
            sa_flags: libc::SA_ONSTACK | libc::SA_SIGINFO | libc::SA_RESTART,
            // copy mask from old handler
            sa_mask: old_action.sa_mask,
        };
        let ret = unsafe { libc::sigaction(signum, &new_action, ptr::null_mut()) };
        if ret == -1 {
            return Err(anyhow::anyhow!(
                "failed to setup `sigaction` for `orb_init_signal_multiplexer`"
            ));
        }

        registry.initialized_set.push(signum);
    }

    // Now, we just have to push to the list.
    if unsafe { orb_push_signal_multiplexer(signum, user_action, userdata) } == 0 {
        return Err(anyhow::anyhow!(
            "failed to push to signal multiplexer (oom?)"
        ));
    }

    Ok(())
}
