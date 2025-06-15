use std::env;
use std::fs::OpenOptions;
use std::net::TcpStream;
use std::os::unix::io::AsRawFd;
use orbblk::{NBD_FLAG_SEND_FLUSH, NBD_FLAG_READ_ONLY};

// NBD ioctl definitions from Linux kernel
const NBD_SET_SOCK: u64 = 0xab00;
const NBD_SET_BLKSIZE: u64 = 0xab01;
const NBD_SET_SIZE: u64 = 0xab02;
const NBD_DO_IT: u64 = 0xab03;
const NBD_CLEAR_SOCK: u64 = 0xab04;
const NBD_CLEAR_QUE: u64 = 0xab05;
const NBD_DISCONNECT: u64 = 0xab08;
const NBD_SET_FLAGS: u64 = 0xab0a;

fn main() -> anyhow::Result<()> {
    let args: Vec<String> = env::args().collect();
    if args.len() != 6 {
        eprintln!("Usage: {} <nbd_device> <server_ip> <size> <block_size> <read_only>", args[0]);
        eprintln!("Example: {} /dev/nbd0 127.0.0.1:10809 1073741824 4096 0", args[0]);
        eprintln!("  read_only: 0 for read-write, 1 for read-only");
        std::process::exit(1);
    }

    let nbd_device = &args[1];
    let server_addr = &args[2];
    let size: u64 = args[3].parse()?;
    let block_size: u64 = args[4].parse()?;
    let read_only: u32 = args[5].parse()?;
    
    if read_only != 0 && read_only != 1 {
        eprintln!("Error: read_only must be 0 or 1");
        std::process::exit(1);
    }

    // Open the NBD device
    let nbd = OpenOptions::new()
        .read(true)
        .write(true)
        .open(nbd_device)?;
    let nbd_fd = nbd.as_raw_fd();

    // Clean up any existing connection
    unsafe {
        // Clear socket (ignore errors)
        libc::ioctl(nbd_fd, NBD_CLEAR_SOCK);
        // Clear queue (ignore errors)
        libc::ioctl(nbd_fd, NBD_CLEAR_QUE);
    }

    // Connect to the NBD server
    let sock = TcpStream::connect(server_addr)?;
    
    // Perform NBD handshake
    // NBD servers typically expect some kind of handshake, but our simple server doesn't implement it
    // For now, we'll just get the socket FD
    let sock_fd = sock.as_raw_fd();

    // Configure the NBD device
    unsafe {
        // Set block size
        if libc::ioctl(nbd_fd, NBD_SET_BLKSIZE, block_size) < 0 {
            return Err(anyhow::anyhow!("Failed to set block size: {}", std::io::Error::last_os_error()));
        }

        // Set size
        if libc::ioctl(nbd_fd, NBD_SET_SIZE, size) < 0 {
            return Err(anyhow::anyhow!("Failed to set size: {}", std::io::Error::last_os_error()));
        }

        // Set socket
        if libc::ioctl(nbd_fd, NBD_SET_SOCK, sock_fd) < 0 {
            return Err(anyhow::anyhow!("Failed to set socket: {}", std::io::Error::last_os_error()));
        }

        // Set flags (enable flush and optionally read-only)
        let mut flags = NBD_FLAG_SEND_FLUSH;
        if read_only == 1 {
            flags |= NBD_FLAG_READ_ONLY;
        }
        if libc::ioctl(nbd_fd, NBD_SET_FLAGS, flags) < 0 {
            return Err(anyhow::anyhow!("Failed to set flags: {}", std::io::Error::last_os_error()));
        }

        println!("NBD device {} configured:", nbd_device);
        println!("  Server: {}", server_addr);
        println!("  Size: {} bytes", size);
        println!("  Block size: {} bytes", block_size);
        println!("  Read-only: {}", if read_only == 1 { "yes" } else { "no" });
        println!("Starting NBD kernel thread...");

        // Start the kernel NBD thread
        // This will block until disconnected
        if libc::ioctl(nbd_fd, NBD_DO_IT) < 0 {
            let error = std::io::Error::last_os_error();
            eprintln!("NBD_DO_IT failed: {}", error);
            if error.raw_os_error() == Some(16) { // EBUSY
                eprintln!("Device is busy. Try:");
                eprintln!("  sudo umount {}", nbd_device);
                eprintln!("  sudo nbd-client -d {}", nbd_device);
                eprintln!("Or check if another process is using the device.");
            }
        }

        // Clear the socket when done
        libc::ioctl(nbd_fd, NBD_CLEAR_SOCK);
    }

    println!("NBD client disconnected");
    Ok(())
}
