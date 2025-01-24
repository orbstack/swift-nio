use std::io::Write;

use super::{pax::PaxHeader, ustar::{OverflowError, TypeFlag, UstarHeader}};

#[derive(Default)]
pub struct Headers {
    tar: UstarHeader,
    pub pax: PaxHeader,
}

impl Headers {
    pub fn write_to(&mut self, w: &mut impl Write) -> anyhow::Result<()> {
        // PAX header precedes real tar header
        if !self.pax.is_empty() {
            self.pax.write_to(w)?;
        }

        w.write_all(self.tar.as_bytes())?;
        Ok(())
    }

    pub fn set_entry_type(&mut self, typ: TypeFlag) {
        self.tar.set_entry_type(typ);
    }

    pub fn set_mode(&mut self, mode: u32) -> Result<(), OverflowError> {
        self.tar.set_mode(mode)
    }

    pub fn set_uid(&mut self, uid: u32) {
        if self.tar.set_uid(uid).is_err() {
            self.pax.add_integer_field("uid", uid);
        }
    }

    pub fn set_gid(&mut self, gid: u32) {
        if self.tar.set_gid(gid).is_err() {
            self.pax.add_integer_field("gid", gid);
        }
    }

    pub fn set_size(&mut self, size: u64) {
        if self.tar.set_size(size).is_err() {
            self.pax.add_integer_field("size", size);
        }
    }

    pub fn set_mtime(&mut self, mtime: i64, mtime_nsec: i64) {
        // if positive and no nsecs, then try tar header first
        // (skip invalid nsecs to avoid breaking number formatting code)
        if mtime >= 0 && (mtime_nsec == 0 || mtime_nsec >= 1_000_000_000) {
            #[allow(clippy::collapsible_if)]
            if self.tar.set_mtime(mtime as u64).is_ok() {
                return;
            }
        }

        self.pax.add_time_field("mtime", mtime, mtime_nsec);
    }

    pub fn set_path(&mut self, path: &[u8]) {
        if self.tar.set_path(path).is_err() {
            self.pax.add_field("path", path);
        }
    }

    pub fn set_device_major(&mut self, major: u32) {
        if self.tar.set_device_major(major).is_err() {
            // POSIX doesn't define a field for this, so follow bsdtar
            self.pax.add_integer_field("SCHILY.devmajor", major);
        }
    }

    pub fn set_device_minor(&mut self, minor: u32) {
        if self.tar.set_device_minor(minor).is_err() {
            // POSIX doesn't define a field for this, so follow bsdtar
            self.pax.add_integer_field("SCHILY.devminor", minor);
        }
    }

    pub fn set_link_path(&mut self, path: &[u8]) {
        if self.tar.set_link_path(path).is_err() {
            // PAX long name extension
            self.pax.add_field("linkpath", path);
            self.tar.set_link_path("././@LongLink".as_bytes()).unwrap();
        }
    }
}
