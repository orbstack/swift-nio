use std::error::Error;

use ifstructs::ifreq;
use nix::{
    ioctl_write_ptr_bad,
    sys::socket::{self, AddressFamily, SockFlag, SockType},
    unistd::close,
};

const SIOCETHTOOL: u32 = 0x8946;

pub const ETHTOOL_STSO: u32 = 0x0000001f;

#[repr(C)]
pub struct EthtoolValue {
    cmd: u32,
    value: u32,
}

mod ioctl {
    use super::*;
    ioctl_write_ptr_bad!(ethtoolset, SIOCETHTOOL, ifreq);
}

pub fn set(name: &str, cmd: u32, value: u32) -> Result<(), Box<dyn Error>> {
    let mut ifr = ifreq::from_name(name)?;
    let mut ev = EthtoolValue { cmd, value };

    ifr.ifr_ifru.ifr_data = (&mut ev as *mut EthtoolValue).cast::<_>();

    let sfd = socket::socket(
        AddressFamily::Netlink,
        SockType::Raw,
        SockFlag::empty(),
        None,
    )?;

    let res = unsafe { ioctl::ethtoolset(sfd, &ifr) };
    close(sfd)?;
    res.map(|_| ())?;
    Ok(())
}
