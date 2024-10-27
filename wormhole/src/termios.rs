use std::os::fd::AsRawFd;

use anyhow::anyhow;
use nix::{
    pty::{openpty, OpenptyResult, Winsize},
    sys::termios::{
        cfsetispeed, cfsetospeed, tcgetattr, tcsetattr, BaudRate, ControlFlags, InputFlags,
        LocalFlags, OutputFlags, SetArg, Termios,
    },
};

use crate::set_cloexec;

pub fn create_pty(w: u16, h: u16, termios_config: Vec<u8>) -> anyhow::Result<OpenptyResult> {
    let pty = openpty(
        Some(&Winsize {
            ws_row: h,
            ws_col: w,
            ws_xpixel: 0,
            ws_ypixel: 0,
        }),
        None,
    )?;

    set_cloexec(pty.master.as_raw_fd())?;

    // read and set termios
    let mut termios = tcgetattr(&pty.slave)?;
    set_termios(&mut termios, &termios_config)?;
    tcsetattr(&pty.slave, SetArg::TCSANOW, &termios)?;

    Ok(pty)
}

pub fn set_termios(termios: &mut Termios, buf: &[u8]) -> anyhow::Result<()> {
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

    for cc in control_chars {
        let val = read_u8(buf, &mut idx)?;
        termios.control_chars[cc] = val;
    }

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
