use std::{error::Error, fs, time::Instant};

const ROSETTA_FINGERPRINT_SALT: &[u8] = b"orb\x00rosetta\x00fp";
const ROSETTA_BUFFER: usize = 524288;

fn start_hash(salt: &[u8], data: &[u8]) -> blake3::Hasher {
    let mut hasher = blake3::Hasher::new();
    hasher.update(salt);
    hasher.update(data);
    hasher
}

fn apply_xof(hasher: &mut blake3::Hasher, patch: &mut [u8]) {
    let mut xof = hasher.finalize_xof();
    let mut buf = [0u8; ROSETTA_BUFFER];
    let mut offset = 0;

    // skip hash (first block)
    xof.set_position(64);

    while offset < patch.len() {
        let len = std::cmp::min(ROSETTA_BUFFER, patch.len() - offset);
        xof.fill(&mut buf);
        // XOR
        for i in 0..len {
            patch[offset + i] ^= buf[i];
        }
        offset += len;
    }
}

fn main() -> Result<(), Box<dyn Error>> {
    // read source data (arg 1)
    let args = std::env::args().collect::<Vec<_>>();
    let source_data = fs::read(&args[1])?;
    let mut patch = fs::read(&args[2])?;

    // encrypt
    let start_time = Instant::now();
    let mut hasher = start_hash(ROSETTA_FINGERPRINT_SALT, &source_data);
    let fingerprint: [u8; 32] = hasher.finalize().into();
    println!("fingerprint: {:?}", hex::encode(fingerprint));
    apply_xof(&mut hasher, &mut patch);
    println!("encryption took {}ms", start_time.elapsed().as_millis());

    // write
    fs::write(&args[3], &patch)?;

    Ok(())
}
