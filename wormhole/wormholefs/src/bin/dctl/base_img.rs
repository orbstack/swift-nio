use std::collections::HashSet;

const BASE_STORE_DIR: &str = "/nix/orb/sys/.base";

fn list_dir<T: FromIterator<String>>(dir: &str) -> anyhow::Result<T> {
    std::fs::read_dir(dir)?
        .map(|entry| {
            let entry = entry?;
            let path = entry.path();
            let name = path.file_name().unwrap().to_str().unwrap().to_string();
            Ok(name)
        })
        .collect()
}

pub fn list<T: FromIterator<String>>() -> anyhow::Result<T> {
    list_dir(BASE_STORE_DIR)
}

// after interrupted install, we might lose paths from base store (because nix deletes existing conflicting paths before extracting them)
// to fix this, list everything in /nix/store, and restore missing paths from base store
// would be much faster to delete whiteout from overlayfs upperdir, but that's not allowed
pub fn restore_missing() -> anyhow::Result<()> {
    let base_paths: HashSet<String> = list()?;
    let current_paths: HashSet<String> = list_dir("/nix/store")?;
    let missing_base_paths = base_paths.difference(&current_paths);

    // copy missing base paths from base
    for path in missing_base_paths {
        eprintln!("restoring missing base path: {}", path);
        let src = format!("{}/{}", BASE_STORE_DIR, path);
        let dest = format!("/nix/store/{}", path);
        copy_dir::copy_dir(&src, &dest)?;
    }

    Ok(())
}
