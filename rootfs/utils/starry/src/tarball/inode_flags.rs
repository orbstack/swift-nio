use smallvec::SmallVec;

use crate::sys::inode_flags::InodeFlags;

pub trait InodeFlagsExt {
    fn add_name(
        &self,
        names: &mut SmallVec<[&'static str; 1]>,
        name: &'static str,
        flag: InodeFlags,
    );

    fn pax_names(&self) -> SmallVec<[&'static str; 1]>;
}

impl InodeFlagsExt for InodeFlags {
    #[inline]
    fn add_name(
        &self,
        names: &mut SmallVec<[&'static str; 1]>,
        name: &'static str,
        flag: InodeFlags,
    ) {
        if self.contains(flag) {
            names.push(name);
        }
    }

    // returning a SmallVec is more efficient for the common 1-flag case: no string joining/allocation required
    fn pax_names(&self) -> SmallVec<[&'static str; 1]> {
        let mut names = SmallVec::<[&'static str; 1]>::new();

        // filter to flags that should be included in archives
        let fl = self.intersection(InodeFlags::ARCHIVE_FLAGS);

        // only include flags supported by bsdtar
        // https://github.com/libarchive/libarchive/blob/4b6dd229c6a931c641bc40ee6d59e99af15a9432/libarchive/archive_entry.c#L1885
        fl.add_name(&mut names, "sappnd", InodeFlags::APPEND);
        fl.add_name(&mut names, "noatime", InodeFlags::NOATIME);
        fl.add_name(&mut names, "compress", InodeFlags::COMPR);
        fl.add_name(&mut names, "nocow", InodeFlags::NOCOW);
        fl.add_name(&mut names, "nodump", InodeFlags::NODUMP);
        fl.add_name(&mut names, "dirsync", InodeFlags::DIRSYNC);
        fl.add_name(&mut names, "schg", InodeFlags::IMMUTABLE);
        fl.add_name(&mut names, "journal", InodeFlags::JOURNAL_DATA);
        fl.add_name(&mut names, "projinherit", InodeFlags::PROJINHERIT);
        fl.add_name(&mut names, "securedeletion", InodeFlags::SECRM);
        fl.add_name(&mut names, "sync", InodeFlags::SYNC);
        fl.add_name(&mut names, "tail", InodeFlags::NOTAIL);
        fl.add_name(&mut names, "topdir", InodeFlags::TOPDIR);
        fl.add_name(&mut names, "undel", InodeFlags::UNRM);

        names
    }
}
