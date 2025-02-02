use bytemuck::{Pod, Zeroable};
use numtoa::NumToA;
use std::cmp::min;

#[repr(C)]
#[derive(Pod, Zeroable, Clone, Copy)]
struct UstarHeaderSerialized {
    // https://pubs.opengroup.org/onlinepubs/007904975/utilities/pax.html#tag_04_100_13_06
    name: [u8; 100],
    mode: [u8; 8],
    uid: [u8; 8],
    gid: [u8; 8],
    size: [u8; 12],
    mtime: [u8; 12],
    chksum: [u8; 8],
    typeflag: [u8; 1],
    linkname: [u8; 100],
    magic: [u8; 6],
    version: [u8; 2],
    uname: [u8; 32],
    gname: [u8; 32],
    devmajor: [u8; 8],
    devminor: [u8; 8],
    prefix: [u8; 155],

    // up to 512 bytes
    _padding: [u8; 12],
}

#[repr(u8)]
pub enum TypeFlag {
    Regular = b'0', // or '\0' for legacy reasons
    HardLink = b'1',
    Symlink = b'2',
    Char = b'3',
    Block = b'4',
    Directory = b'5',
    Fifo = b'6',
    HighPerformance = b'7', // = Regular
    PaxExtendedHeader = b'x',
    PaxGlobalHeader = b'g',
}

#[derive(Debug, PartialEq, Eq)]
pub struct OverflowError {}

impl std::error::Error for OverflowError {}

impl std::fmt::Display for OverflowError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "value too large for field")
    }
}

pub struct UstarHeader {
    data: UstarHeaderSerialized,
}

impl Default for UstarHeader {
    fn default() -> Self {
        let mut header = Self {
            data: UstarHeaderSerialized::zeroed(),
        };

        header.data.magic = *b"ustar\0";
        header.data.version = [b'0'; 2];
        header
    }
}

impl UstarHeader {
    pub fn set_entry_type(&mut self, typ: TypeFlag) {
        self.data.typeflag = [typ as u8; 1];
    }

    pub fn set_path(&mut self, path: &[u8]) -> Result<(), OverflowError> {
        match path.len() {
            // standard tar: up to 100 bytes
            0..=100 => {
                self.data.name[..path.len()].copy_from_slice(path);
            }
            // ustar prefix: 155 + 100 bytes
            101..=255 => {
                // final path = prefix + '/' + path, so we have to find a / to split on

                // get the prefix part of the string
                let prefix_path = &path[..min(path.len(), 155)];
                // split at last / in prefix section
                let mut split_iter = prefix_path.rsplitn(2, |&c| c == b'/');
                let prefix = split_iter.nth(1).ok_or(OverflowError {})?;
                // take the entire rest of the string as the name
                let name = path.strip_prefix(prefix).unwrap().strip_prefix(b"/").unwrap();

                if prefix.len() > 155 || name.len() > 100 {
                    // not splittable: path component is too long
                    return Err(OverflowError {});
                }

                // copy prefix
                self.data.prefix[..prefix.len()].copy_from_slice(prefix);
                // copy path
                self.data.name[..name.len()].copy_from_slice(name);
            }
            _ => return Err(OverflowError {}),
        }
        Ok(())
    }

    pub fn set_link_path(&mut self, path: &[u8]) -> Result<(), OverflowError> {
        if path.len() > 100 {
            return Err(OverflowError {});
        }

        self.data.linkname[..path.len()].copy_from_slice(path);
        Ok(())
    }

    pub fn set_mode(&mut self, mode: u32) -> Result<(), OverflowError> {
        write_left_padded(&mut self.data.mode, mode, 8, 8)
    }

    pub fn set_uid(&mut self, uid: u32) -> Result<(), OverflowError> {
        write_left_padded(&mut self.data.uid, uid, 8, 8)
    }

    pub fn set_gid(&mut self, gid: u32) -> Result<(), OverflowError> {
        write_left_padded(&mut self.data.gid, gid, 8, 8)
    }

    pub fn set_size(&mut self, size: u64) -> Result<(), OverflowError> {
        write_left_padded(&mut self.data.size, size, 8, 12)
    }

    pub fn set_mtime(&mut self, mtime: u64) -> Result<(), OverflowError> {
        write_left_padded(&mut self.data.mtime, mtime, 8, 12)
    }

    pub fn set_device_major(&mut self, major: u32) -> Result<(), OverflowError> {
        write_left_padded(&mut self.data.devmajor, major, 8, 8)
    }

    pub fn set_device_minor(&mut self, minor: u32) -> Result<(), OverflowError> {
        write_left_padded(&mut self.data.devminor, minor, 8, 8)
    }

    fn set_checksum(&mut self) {
        // checksum = sum of all octets, with checksum field set to spaces
        self.data.chksum = [b' '; 8];

        // spec: must be at least 17 bits
        let mut sum: u32 = 0;
        for b in bytemuck::bytes_of(&self.data) {
            sum += *b as u32;
        }
        write_left_padded(&mut self.data.chksum, sum, 8, 8).unwrap();
    }

    pub fn as_bytes(&mut self) -> &[u8] {
        // calculate checksum
        self.set_checksum();

        bytemuck::bytes_of(&self.data)
    }
}

pub fn write_left_padded<T: NumToA<T>>(
    out_buf: &mut [u8],
    val: T,
    base: T,
    target_len: usize,
) -> Result<(), OverflowError> {
    // stack array for max possible length
    let mut unpadded_buf: [u8; 32] = [0; 32];
    let formatted = val.numtoa(base, &mut unpadded_buf);

    // fill leading space with zeros
    let target_buf = &mut out_buf[..target_len];
    if formatted.len() > target_len {
        return Err(OverflowError {});
    }
    let padding_len = target_len - formatted.len();
    target_buf[padding_len..].copy_from_slice(formatted);
    target_buf[..padding_len].fill(b'0');
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn parse_tar_string(data: &[u8]) -> &str {
        std::str::from_utf8(data).unwrap().trim_end_matches('\0')
    }

    trait HeaderTestExt {
        fn assert_final_path(&self, expected: &str);
    }

    impl HeaderTestExt for UstarHeader {
        fn assert_final_path(&self, expected: &str) {
            let prefix = parse_tar_string(&self.data.prefix);
            let name = parse_tar_string(&self.data.name);
            let final_path = if prefix.is_empty() {
                name.to_string()
            } else {
                format!("{}/{}", prefix, name)
            };
            assert_eq!(final_path, expected);
        }
    }

    fn test_ustar_path_ok(path: &str) {
        let mut header = UstarHeader::default();
        let result = header.set_path(path.as_bytes());
        assert_eq!(result, Ok(()));
        header.assert_final_path(path);
    }

    fn test_ustar_path_fail(path: &str) {
        let mut header = UstarHeader::default();
        let result = header.set_path(path.as_bytes());
        assert_eq!(result, Err(OverflowError {}));
    }

    #[test]
    fn test_ustar_path_fail_single_101() {
        test_ustar_path_fail(&"a".repeat(101));
    }

    #[test]
    fn test_ustar_path_ok_single_100() {
        test_ustar_path_ok(&"a".repeat(100));
    }

    #[test]
    fn test_ustar_path_ok_multi_101() {
        test_ustar_path_ok(&"a/b".repeat(50));
    }

    #[test]
    fn test_ustar_path_ok_nix1() {
        test_ustar_path_ok("stress/rootfs/var/lib/docker/volumes/wormhole-data/_data/upper/nix/store/c05d1sqfhkl93p3j5ykic68mgg1gsrvb-source/pkgs/development/python-modules/hurry-filesize/use-pep-420-implicit-namespace-package.patch");
    }

    #[test]
    fn test_ustar_path_ok_nix2() {
        test_ustar_path_ok("rootfs/var/lib/docker/volumes/wormhole-data/_data/upper/nix/store/c05d1sqfhkl93p3j5ykic68mgg1gsrvb-source/lib/tests/packages-from-directory/my-namespace/my-sub-namespace/g.nix");
    }

    #[test]
    fn test_ustar_path_ok_nix3() {
        test_ustar_path_ok("rootfs/var/lib/docker/volumes/wormhole-data/_data/upper/nix/store/c05d1sqfhkl93p3j5ykic68mgg1gsrvb-source/lib/tests/packages-from-directory/my-namespace/f/");
    }

    // technically I think this is possible to support (prefix='a'*155, name='') but it's probably an edge case that breaks some extractors, so we don't support it
    #[test]
    fn test_ustar_path_ok_155_trailing() {
        test_ustar_path_ok(&("a".repeat(155) + "/"));
    }
}
