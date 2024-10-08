use std::{ops::Range, os::fd::AsRawFd, slice};

use crate::virtio::descriptor_utils::Iovec;

use super::{device::DiskProperties, SECTOR_SIZE};

impl DiskProperties {
    fn validated_regions(&self) -> impl Iterator<Item = Range<u64>> {
        // We only want to validate the GPT regions since we've found that, if this bug occurs, it
        // immediately occurs for GPT region and immediately causes a crash.
        //
        // See https://uefi.org/specs/UEFI/2.10/05_GUID_Partition_Table_Format.html for details on
        // GPT layout.
        [
            // First two logical blocks
            0..2 * SECTOR_SIZE,
            // Last logical block
            (self.size() - SECTOR_SIZE)..self.size(),
        ]
        .into_iter()
    }

    pub(super) fn hook_disk_read(&self, offset: usize, iovec: &Iovec) {
        // Check whether this region requires validation
        let range_overlap = self.validated_regions().any(|r| {
            range_overlap(
                r.clone(),
                (offset as u64)..(offset as u64 + iovec.len() as u64),
            )
        });

        if !range_overlap {
            return;
        }

        // Read the file again
        // We don't reduce the range to just the range of interest because we want to compare apples
        // to apples. Also these reads are probably fairly small so it just doesn't really matter.
        let file = self.file().as_raw_fd();

        let mut validation_buf = Vec::<u8>::with_capacity(iovec.len());
        let validation_iov = libc::iovec {
            iov_base: validation_buf.as_mut_ptr().cast(),
            iov_len: iovec.len(),
        };

        let res = unsafe { libc::preadv(file, &validation_iov, 1, offset as i64) };

        if res != iovec.len() as isize {
            tracing::error!(
                "failed to re-run read for access id={:?} offset={}, iov_len={}: got read len {}",
                self.image_id(),
                offset,
                iovec.len(),
                res,
            );
            return;
        }

        unsafe { validation_buf.set_len(iovec.len()) };

        // Compare the buffers
        // Technically unsound because the `iovec` is backed by guest memory but we're already playing
        // fast and loose with these rules and a well behaved guest should not concurrently modify a
        // buffer they passed to a device.
        let mmap = unsafe { slice::from_raw_parts(iovec.as_ptr(), iovec.len()) };
        let pread = &*validation_buf;

        if mmap != pread {
            tracing::error!(
                "failed to re-run read for access id={:?} offset={}, iov_len={}: \
                 discrepancy between GPT reads\nmmap: {mmap:?}\npread: {pread:?}",
                self.image_id(),
                offset,
                iovec.len(),
            );
        } else if mmap.iter().all(|&v| v == 0) {
            tracing::error!(
                "failed to re-run read for access id={:?} offset={}, iov_len={}: \
                 both mmap and pread report a GPT of all zeroes",
                self.image_id(),
                offset,
                iovec.len(),
            );
        }
    }
}

fn range_overlap(x: Range<u64>, y: Range<u64>) -> bool {
    x.start < y.end && y.start < x.end
}
