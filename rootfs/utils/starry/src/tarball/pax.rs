use std::io::Write;

use smallvec::SmallVec;

use super::{context::TAR_PADDING, ustar::{write_left_padded, TypeFlag, UstarHeader}};

const PAX_HEADER_NAME: &str = "@PaxHeader";

pub struct PaxHeader {
    header: UstarHeader,
    data: Vec<u8>,
}

impl Default for PaxHeader {
    fn default() -> Self {
        let mut header = UstarHeader::default();
        header.set_entry_type(TypeFlag::PaxExtendedHeader);
        // name="@PaxHeader": doesn't match bsdtar or GNU tar behavior, but spec doesn't care and this is faster
        header.set_path(PAX_HEADER_NAME.as_bytes()).unwrap();

        Self {
            header,
            data: Vec::with_capacity(1024),
        }
    }
}

impl PaxHeader {
    pub fn add_field<K: AsRef<[u8]> + ?Sized>(&mut self, key: &K, value: &[u8]) {
        // +3: space, equals, newline
        let key = key.as_ref();
        let payload_len = key.len() + value.len() + 3;

        // how many digits are in the length?
        let payload_len_digits = (payload_len.ilog10() + 1) as usize;
        let mut total_len = payload_len + payload_len_digits;
        // if payload_len=99, this might add a digit
        let total_len_digits = (total_len.ilog10() + 1) as usize;
        if total_len_digits > payload_len_digits {
            // add space for one more digit
            total_len += 1;
        }

        let mut itoa_buf = itoa::Buffer::new();
        let len_str = itoa_buf.format(total_len);

        // {len_str} {key}={value}\n
        self.data.extend_from_slice(len_str.as_bytes());
        self.data.push(b' ');
        self.data.extend_from_slice(key);
        self.data.push(b'=');
        self.data.extend_from_slice(value);
        self.data.push(b'\n');
    }

    pub fn add_integer_field<T: itoa::Integer>(&mut self, key: &str, val: T) {
        let mut buf = itoa::Buffer::new();
        self.add_field(key, buf.format(val).as_bytes());
    }

    pub fn add_time_field(&mut self, key: &str, seconds: i64, nanos: i64) {
        // "18446744073709551616.000000000" (u64::MAX + 9 digits for nanoseconds)
        let mut time_buf = SmallVec::<[u8; 30]>::new();
        let mut dec_buf = itoa::Buffer::new();
        let seconds = dec_buf.format(seconds);
        time_buf.extend_from_slice(seconds.as_bytes());
        time_buf.push(b'.');

        let nanos_start = time_buf.len();
        time_buf.resize(nanos_start + 9, 0);
        // can't overflow: we checked that nsec < 1e9
        write_left_padded(
            &mut time_buf[nanos_start..],
            nanos as u64,
            10,
            9,
        )
        .unwrap();

        self.add_field(key, &time_buf);
    }

    pub fn is_empty(&self) -> bool {
        self.data.is_empty()
    }

    pub fn write_to(&mut self, w: &mut impl Write) -> anyhow::Result<()> {
        self.header.set_size(self.data.len() as u64).unwrap();
        w.write_all(self.header.as_bytes())?;
        w.write_all(&self.data)?;

        // pad tar to 512 byte block
        let pad = 512 - (self.data.len() % 512);
        if pad != 512 {
            w.write_all(&TAR_PADDING[..pad])?;
        }

        Ok(())
    }
}
