use std::{fmt, ops::Range, os::fd::AsRawFd, slice};

use crate::virtio::descriptor_utils::Iovec;

use super::device::DiskProperties;

impl DiskProperties {
    fn validated_regions(&self) -> impl Iterator<Item = Range<u64>> {
        let logical_block_size = self.fs_block_size;

        // We only want to validate the GPT regions since we've found that, if this bug occurs, it
        // immediately occurs for GPT region and immediately causes a crash.
        //
        // See https://uefi.org/specs/UEFI/2.10/05_GUID_Partition_Table_Format.html for details on
        // GPT layout.
        [
            // First two logical blocks
            0..logical_block_size * 2,
            // Last logical block
            (self.size() - logical_block_size)..self.size(),
        ]
        .into_iter()
    }

    pub(super) fn hook_disk_read(&self, offset: usize, iovec: &Iovec) {
        // Check whether this region requires validation
        let Some(range_overlap) = self.validated_regions().find(|r| {
            range_overlap(
                r.clone(),
                (offset as u64)..(offset as u64 + iovec.len() as u64),
            )
        }) else {
            return;
        };

        // Read the file again. We don't reduce the range to just the range of interest because we
        // want to compare reads starting from the same offset.
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
        let mmap = unsafe { slice::from_raw_parts_mut(iovec.as_mut_ptr(), iovec.len()) };
        let pread = &*validation_buf;

        // Trim buffers
        let range_intersect = range_intersection(
            offset as u64..(offset as u64 + iovec.len() as u64),
            range_overlap,
        );

        let trim_start = (range_intersect.start - offset as u64) as usize;
        let trim_len = (range_intersect.end - range_intersect.start) as usize;
        let mmap = &mut mmap[trim_start..][..trim_len];
        let pread = &pread[trim_start..][..trim_len];

        // Log the GPT entry
        if mmap != pread {
            tracing::error!(
                "failed to re-run read for access id={:?} offset={}, iov_len={}: \
                 discrepancy between GPT reads\nmmap=\n{}\n\npread=\n{}",
                self.image_id(),
                offset,
                iovec.len(),
                DumpBuf(mmap),
                DumpBuf(pread),
            );
        } else {
            tracing::info!(
                "GPT region read id={:?} offset={}, iov_len={}, data=\n{}",
                self.image_id(),
                offset,
                iovec.len(),
                DumpBuf(mmap),
            )
        }
    }
}

fn range_overlap(x: Range<u64>, y: Range<u64>) -> bool {
    x.start < y.end && y.start < x.end
}

fn range_intersection(a: Range<u64>, b: Range<u64>) -> Range<u64> {
    a.start.max(b.start)..a.end.min(b.end)
}

struct DumpBuf<'a>(&'a [u8]);

impl fmt::Display for DumpBuf<'_> {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        use base64::{display::Base64Display, engine::general_purpose::STANDARD_NO_PAD};

        // 6*64 is divisible by 8 so we don't need any padding.
        let line_byte_count = 64;

        let mut lines = self.0.chunks(line_byte_count).peekable();

        loop {
            // Skip until the first line that's not entirely zeroes
            let mut skipped_bytes = 0;

            while lines.peek().is_some_and(|v| is_entirely_zeroes(v)) {
                lines.next();
                skipped_bytes += line_byte_count;
            }

            if skipped_bytes > 0 {
                write!(f, "[skipped {skipped_bytes} zero bytes]")?;
            }

            let Some(line) = lines.next() else {
                break;
            };

            if skipped_bytes > 0 {
                writeln!(f)?;
            }

            write!(f, "{}", Base64Display::new(line, &STANDARD_NO_PAD))?;

            if lines.peek().is_some() {
                writeln!(f)?;
            }
        }

        Ok(())
    }
}

fn is_entirely_zeroes(v: &[u8]) -> bool {
    v.iter().all(|&v| v == 0)
}
