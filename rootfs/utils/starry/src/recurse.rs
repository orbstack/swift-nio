use std::{ffi::CStr, os::fd::OwnedFd};

use crate::{
    buffer_stack::BufferStack,
    sys::getdents::{for_each_getdents, DirEntry},
};

#[derive(thiserror::Error, Debug)]
#[error("{path}: {source}")]
pub struct RecurseError {
    pub path: String,
    pub source: anyhow::Error,
}

pub struct Recurser {
    buffer_stack: BufferStack,
}

fn append_path_to_error(component: &CStr, mut e: anyhow::Error) -> anyhow::Error {
    let string = component.to_string_lossy().to_string();
    if let Some(err) = e.downcast_mut::<RecurseError>() {
        err.path = string + "/" + &err.path;
        e
    } else {
        RecurseError {
            path: string,
            source: e,
        }
        .into()
    }
}

impl Recurser {
    pub fn new() -> anyhow::Result<Self> {
        Ok(Self {
            buffer_stack: BufferStack::new()?,
        })
    }

    pub fn walk_dir(
        &self,
        dirfd: &OwnedFd,
        nents_hint: Option<usize>,
        mut entry_fn: impl FnMut(&DirEntry) -> anyhow::Result<()>,
    ) -> anyhow::Result<()> {
        for_each_getdents(
            dirfd,
            nents_hint,
            &self.buffer_stack,
            |entry| match entry_fn(&entry) {
                Ok(_) => Ok(()),
                Err(e) => Err(append_path_to_error(entry.name, e)),
            },
        )
    }

    pub fn walk_dir_root(
        &self,
        dirfd: &OwnedFd,
        path: &CStr,
        nents_hint: Option<usize>,
        entry_fn: impl FnMut(&DirEntry) -> anyhow::Result<()>,
    ) -> anyhow::Result<()> {
        match self.walk_dir(dirfd, nents_hint, entry_fn) {
            Ok(_) => Ok(()),
            Err(e) => Err(append_path_to_error(path, e)),
        }
    }
}
