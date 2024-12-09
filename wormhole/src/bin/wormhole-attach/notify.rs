use std::path::Path;

use nix::sys::inotify::{AddWatchFlags, InitFlags, Inotify};

// assumption: parent dir exists
pub fn wait_for_path_exist(path: &Path) -> anyhow::Result<()> {
    // optimistically check whether the path already exists
    if path.exists() {
        return Ok(());
    }

    let inotify = Inotify::init(InitFlags::IN_CLOEXEC)?;
    let parent_dir = path.parent().unwrap();
    inotify.add_watch(parent_dir, AddWatchFlags::IN_CREATE)?;

    // now that we've added the watch, check again in case it was created already
    if path.exists() {
        return Ok(());
    }

    let name = path.file_name().unwrap();
    loop {
        let events = inotify.read_events()?;
        for event in events {
            if event.mask == AddWatchFlags::IN_CREATE && event.name == Some(name.into()) {
                return Ok(());
            }
        }
    }
}
