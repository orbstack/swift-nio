use std::os::fd::{AsRawFd, FromRawFd, OwnedFd};

use anyhow::anyhow;
use libc::{O_CLOEXEC, O_NOCTTY, O_RDWR, TIOCGPTPEER, TIOCSPTLCK, TIOCSWINSZ};
use nix::{
    fcntl::{open, OFlag},
    libc::ioctl,
    pty::Winsize,
    sys::{
        stat::Mode,
        termios::{
            cfsetispeed, cfsetospeed, tcgetattr, tcsetattr, BaudRate, ControlFlags, InputFlags,
            LocalFlags, OutputFlags, SetArg, Termios,
        },
    },
};

const CONTROL_CHARS: &[usize] = &[
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
const INPUT_FLAGS: &[InputFlags] = &[
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

const LOCAL_FLAGS: &[LocalFlags] = &[
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

const OUTPUT_FLAGS: &[OutputFlags] = &[
    OutputFlags::OPOST,
    // OutputFlags::OLCUC,
    OutputFlags::ONLCR,
    OutputFlags::OCRNL,
    OutputFlags::ONOCR,
    OutputFlags::ONLRET,
];

const CONTROL_FLAGS: &[ControlFlags] = &[
    ControlFlags::CS5,
    ControlFlags::CS6,
    ControlFlags::CS7,
    ControlFlags::CS8,
    ControlFlags::PARENB,
    ControlFlags::PARODD,
];

pub fn resize_pty(pty: &OwnedFd, rows: u16, cols: u16) -> anyhow::Result<()> {
    let ws = Winsize {
        ws_row: rows,
        ws_col: cols,
        ws_xpixel: 0,
        ws_ypixel: 0,
    };

    let res = unsafe { ioctl(pty.as_raw_fd(), TIOCSWINSZ, &ws) };
    if res < 0 {
        return Err(anyhow!("error resizing pty"));
    }

    Ok(())
}

pub fn create_pty(
    rows: u16,
    cols: u16,
    termios_config: Vec<u8>,
) -> anyhow::Result<(OwnedFd, OwnedFd)> {
    // open master in cloexec-safe manner
    let master = open(
        "/dev/ptmx",
        OFlag::O_RDWR | OFlag::O_NOCTTY | OFlag::O_CLOEXEC,
        Mode::from_bits_truncate(0),
    )?;

    let mut value = 0;
    let res = unsafe { ioctl(master.as_raw_fd(), TIOCSPTLCK, &mut value as *mut i32) };
    if res < 0 {
        return Err(anyhow!("error unlocking pty"));
    }

    // open tty slave peer with cloexec
    let slave = unsafe {
        ioctl(
            master.as_raw_fd(),
            TIOCGPTPEER,
            O_RDWR | O_NOCTTY | O_CLOEXEC,
        )
    };
    if slave < 0 {
        return Err(anyhow!("error obtaining pty slave fd"));
    }

    let slave = unsafe { OwnedFd::from_raw_fd(slave) };

    // read and set termios
    let mut termios = tcgetattr(&slave)?;
    set_termios(&mut termios, &termios_config)?;
    tcsetattr(&slave, SetArg::TCSANOW, &termios)?;

    // set initial pty size
    resize_pty(&slave, rows, cols)?;

    Ok((master, slave))
}

pub fn set_termios(termios: &mut Termios, buf: &[u8]) -> anyhow::Result<()> {
    let mut idx = 0;

    for cc in CONTROL_CHARS {
        let val = read_u8(buf, &mut idx)?;
        termios.control_chars[*cc] = val;
    }

    for flag in INPUT_FLAGS {
        let val = read_u8(buf, &mut idx)?;
        termios.input_flags.set(*flag, val != 0);
    }

    for flag in LOCAL_FLAGS {
        let val = read_u8(buf, &mut idx)?;
        termios.local_flags.set(*flag, val != 0);
    }

    for flag in OUTPUT_FLAGS {
        let val = read_u8(buf, &mut idx)?;
        termios.output_flags.set(*flag, val != 0);
    }
    for flag in CONTROL_FLAGS {
        let val = read_u8(buf, &mut idx)?;
        termios.control_flags.set(*flag, val != 0);
    }

    let ispeed = read_u32(buf, &mut idx)?;
    let ospeed = read_u32(buf, &mut idx)?;

    cfsetispeed(termios, map_speed(ispeed)?)?;
    cfsetospeed(termios, map_speed(ospeed)?)?;
    Ok(())
}

fn read_u8(buf: &[u8], idx: &mut usize) -> anyhow::Result<u8> {
    if buf.len() < *idx + 1 {
        return Err(anyhow!("could not read full termios settings"));
    }
    let val = buf[*idx];
    *idx += 1;
    Ok(val)
}

fn read_u32(buf: &[u8], idx: &mut usize) -> anyhow::Result<u32> {
    if buf.len() < *idx + 4 {
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
