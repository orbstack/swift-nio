use std::{
    io,
    os::{
        self,
        fd::{AsRawFd, RawFd},
    },
};

use anyhow::anyhow;
use colored::control;
use libc::VINTR;
use nix::sys::{
    socket::{recv, MsgFlags},
    termios::{
        self, cfsetispeed, cfsetospeed, BaudRate, ControlFlags, InputFlags, LocalFlags,
        OutputFlags, Termios,
    },
};
use tracing::trace;
pub fn set_termios_to_host(fd: RawFd, termios: &mut Termios) -> anyhow::Result<()> {
    let len = {
        let mut len_bytes = [0_u8; size_of::<u32>()];
        recv(fd, &mut len_bytes, MsgFlags::MSG_WAITALL)?;
        u32::from_be_bytes(len_bytes) as usize
    };

    let mut termios_buf = vec![0_u8; len];
    recv(fd, &mut termios_buf, MsgFlags::MSG_WAITALL)?;
    parse_termios(&termios_buf, termios)
}

pub fn parse_termios(buf: &[u8], termios: &mut Termios) -> anyhow::Result<()> {
    //   there are a couple of control chars that don't exist.. maybe just arch?
    let control_chars = [
        libc::VINTR,
        libc::VQUIT,
        libc::VERASE,
        libc::VKILL,
        libc::VEOF,
        libc::VEOL,
        libc::VEOL2,
        libc::VSTART,
        libc::VSTOP,
        libc::VSUSP,
        // libc::VDSUSP,
        libc::VREPRINT,
        libc::VWERASE,
        libc::VLNEXT,
        // libc::VFLUSH,
        // libc::VSWTCH,
        // libc::VSTATUS,
        libc::VDISCARD,
    ];
    let input_flags = [
        InputFlags::IGNPAR,
        InputFlags::PARMRK,
        InputFlags::INPCK,
        InputFlags::ISTRIP,
        InputFlags::INLCR,
        InputFlags::IGNCR,
        InputFlags::ICRNL,
        // InputFlags::IUCLC,
        InputFlags::IXON,
        InputFlags::IXANY,
        InputFlags::IXOFF,
        InputFlags::IMAXBEL,
        InputFlags::IUTF8,
    ];

    let local_flags = [
        LocalFlags::ISIG,
        LocalFlags::ICANON,
        // LocalFlags::XCASE,
        LocalFlags::ECHO,
        LocalFlags::ECHOE,
        LocalFlags::ECHOK,
        LocalFlags::ECHONL,
        LocalFlags::NOFLSH,
        LocalFlags::TOSTOP,
        LocalFlags::IEXTEN,
        LocalFlags::ECHOCTL,
        LocalFlags::ECHOKE,
        LocalFlags::PENDIN,
    ];

    let output_flags = [
        OutputFlags::OPOST,
        // OutputFlags::OLCUC,
        OutputFlags::ONLCR,
        OutputFlags::OCRNL,
        OutputFlags::ONOCR,
        OutputFlags::ONLRET,
    ];

    let control_flags = [
        ControlFlags::CS5,
        ControlFlags::CS6,
        ControlFlags::CS7,
        ControlFlags::CS8,
        ControlFlags::PARENB,
        ControlFlags::PARODD,
    ];

    let mut idx = 0;

    // trace!("reading control chars");
    for cc in control_chars {
        let val = read_u8(buf, &mut idx)?;
        termios.control_chars[cc] = val;
    }

    // trace!("intr {:?}", termios.control_chars);

    // trace!("reading input flags");
    for flag in input_flags {
        let val = read_u8(buf, &mut idx)?;
        assert!(val == 0 || val == 1);
        termios.input_flags.set(flag, val != 0);
    }

    for flag in local_flags {
        let val = read_u8(buf, &mut idx)?;
        assert!(val == 0 || val == 1);
        termios.local_flags.set(flag, val != 0);
    }

    for flag in output_flags {
        let val = read_u8(buf, &mut idx)?;
        assert!(val == 0 || val == 1);
        termios.output_flags.set(flag, val != 0);
    }
    for flag in control_flags {
        let val = read_u8(buf, &mut idx)?;
        assert!(val == 0 || val == 1);
        termios.control_flags.set(flag, val != 0);
    }

    let ispeed = read_u32(buf, &mut idx)?;
    let ospeed = read_u32(buf, &mut idx)?;

    cfsetispeed(termios, map_speed(ispeed)?)?;
    cfsetospeed(termios, map_speed(ospeed)?)?;
    Ok(())
}

fn read_u8(buf: &[u8], idx: &mut usize) -> anyhow::Result<u8> {
    if *idx >= buf.len() {
        return Err(anyhow!("could not read full termios settings"));
    }
    let val = buf[*idx];
    *idx += 1;
    Ok(val)
}

fn read_u32(buf: &[u8], idx: &mut usize) -> anyhow::Result<u32> {
    if *idx + 3 >= buf.len() {
        return Err(anyhow!("could not read full termios settings"));
    }
    let val = u32::from_be_bytes(buf[*idx..*idx + 4].try_into()?);
    *idx += 4;
    Ok(val)
}

fn map_speed(speed: u32) -> anyhow::Result<BaudRate> {
    match BaudRate::try_from(speed) {
        Ok(baudrate) => Ok(baudrate),
        Err(_) => Ok(BaudRate::B9600),
    }
}
