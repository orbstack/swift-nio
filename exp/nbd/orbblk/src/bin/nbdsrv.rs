use std::env;
use std::io::{Read, Write};
use std::mem::MaybeUninit;
use std::net::{TcpListener, TcpStream};
use std::os::unix::io::AsRawFd;
use orbblk::{
    BeU64, NbdRequest, NbdSimpleReply,
    NBD_REQUEST_MAGIC, NBD_SIMPLE_REPLY_MAGIC,
    NBD_CMD_READ, NBD_CMD_WRITE, NBD_CMD_DISC, NBD_CMD_FLUSH
};

// macOS disk ioctls
const DKIOCGETBLOCKSIZE: u64 = 0x40046418;
const DKIOCGETBLOCKCOUNT: u64 = 0x40086419;
const DKIOCISWRITABLE: u64 = 0x4004641d;
const DKIOCSYNCHRONIZE: u64 = 0x20006416;

struct NbdServer {
    device_path: String,
    bind_addr: String,
    device: std::fs::File,
}

impl NbdServer {
    fn new(device_path: String) -> anyhow::Result<Self> {
        let device = std::fs::OpenOptions::new()
            .read(true)
            .write(true)
            .open(&device_path)?;
            
        Ok(Self {
            device_path,
            bind_addr: "127.0.0.1:10809".to_string(),
            device,
        })
    }
    
    fn get_disk_info(&self) -> anyhow::Result<()> {
        let fd = self.device.as_raw_fd();
        
        unsafe {
            // Get block size
            let mut block_size: u32 = 0;
            if libc::ioctl(fd, DKIOCGETBLOCKSIZE, &mut block_size as *mut u32) < 0 {
                return Err(anyhow::anyhow!("Failed to get block size: {}", std::io::Error::last_os_error()));
            }
            
            // Get block count
            let mut block_count: u64 = 0;
            if libc::ioctl(fd, DKIOCGETBLOCKCOUNT, &mut block_count as *mut u64) < 0 {
                return Err(anyhow::anyhow!("Failed to get block count: {}", std::io::Error::last_os_error()));
            }
            
            // Check if writable
            let mut writable: u32 = 0;
            if libc::ioctl(fd, DKIOCISWRITABLE, &mut writable as *mut u32) < 0 {
                return Err(anyhow::anyhow!("Failed to check if writable: {}", std::io::Error::last_os_error()));
            }
            
            let size = block_size as u64 * block_count;
            let read_only = if writable != 0 { 0 } else { 1 };
            
            println!("Disk information for {}:", self.device_path);
            println!("  Block size: {} bytes", block_size);
            println!("  Block count: {}", block_count);
            println!("  Total size: {} bytes ({:.2} GB)", size, size as f64 / 1073741824.0);
            println!("  Writable: {}", if writable != 0 { "yes" } else { "no" });
            println!();
            println!("To connect from Linux, run:");
            println!("  sudo nbdclient /dev/nbd0 {} {} {} {}", self.bind_addr, size, block_size, read_only);
        }
        
        Ok(())
    }
    
    fn run(&self) -> anyhow::Result<()> {
        self.get_disk_info()?;
        
        let listener = TcpListener::bind(&self.bind_addr)?;
        println!();
        println!("NBD server listening on {}, serving {}", self.bind_addr, self.device_path);
        
        for stream in listener.incoming() {
            match stream {
                Ok(stream) => {
                    if let Err(e) = self.handle_client(stream) {
                        eprintln!("Client error: {}", e);
                    }
                }
                Err(e) => eprintln!("Connection error: {}", e),
            }
        }
        
        Ok(())
    }
    
    fn handle_client(&self, mut stream: TcpStream) -> anyhow::Result<()> {
        // Set TCP_NODELAY for better performance
        stream.set_nodelay(true)?;
        
        let mut request_buf = [0u8; std::mem::size_of::<NbdRequest>()];
        let mut buffer = Vec::<MaybeUninit<u8>>::new();
        
        loop {
            match stream.read_exact(&mut request_buf) {
                Ok(_) => {},
                Err(e) if e.kind() == std::io::ErrorKind::UnexpectedEof => {
                    // Clean disconnect
                    println!("Client disconnected (EOF)");
                    break;
                }
                Err(e) => {
                    eprintln!("Error reading request: {}", e);
                    return Err(e.into());
                }
            }
            
            let request: &NbdRequest = bytemuck::from_bytes(&request_buf);
            
            if request.magic.get() != NBD_REQUEST_MAGIC {
                return Err(anyhow::anyhow!("Invalid request magic: 0x{:08x}", request.magic.get()));
            }
            
            let command = request.type_.get();
            let cookie = request.cookie;
            let offset = request.offset.get();
            let length = request.length.get();
            
            match command {
                NBD_CMD_READ => {
                    self.handle_read(&mut stream, &mut buffer, cookie, offset, length)?;
                }
                NBD_CMD_WRITE => {
                    self.handle_write(&mut stream, &mut buffer, cookie, offset, length)?;
                }
                NBD_CMD_FLUSH => {
                    self.handle_flush(&mut stream, cookie)?;
                }
                NBD_CMD_DISC => {
                    println!("Client disconnected");
                    break;
                }
                _ => {
                    eprintln!("Unsupported command: {}, disconnecting client", command);
                    break;
                }
            }
        }
        
        Ok(())
    }
    
    fn handle_read(&self, stream: &mut TcpStream, buffer: &mut Vec<MaybeUninit<u8>>, cookie: BeU64, offset: u64, length: u32) -> anyhow::Result<()> {
        buffer.reserve(length as usize);
        let fd = self.device.as_raw_fd();
        
        let bytes_read = unsafe {
            libc::pread(fd, buffer.as_mut_ptr() as *mut libc::c_void, length as usize, offset as libc::off_t)
        };
        
        let error = if bytes_read == -1 {
            let errno = std::io::Error::last_os_error().raw_os_error().unwrap_or(0) as u32;
            eprintln!("Read error at offset {}, length {}: {} (errno {})", 
                     offset, length, std::io::Error::last_os_error(), errno);
            errno
        } else if bytes_read as u32 != length {
            eprintln!("Partial read at offset {}: requested {}, got {}", 
                     offset, length, bytes_read);
            libc::EIO as u32
        } else {
            0u32
        };
        
        let reply = NbdSimpleReply {
            magic: NBD_SIMPLE_REPLY_MAGIC.into(),
            error: error.into(),
            cookie,
        };
        
        stream.write_all(bytemuck::bytes_of(&reply))?;
        
        if error == 0 {
            unsafe { 
                buffer.set_len(bytes_read as usize);
                let initialized_slice = std::slice::from_raw_parts(buffer.as_ptr() as *const u8, bytes_read as usize);
                stream.write_all(initialized_slice)?;
            }
        }
        
        Ok(())
    }
    
    fn handle_write(&self, stream: &mut TcpStream, buffer: &mut Vec<MaybeUninit<u8>>, cookie: BeU64, offset: u64, length: u32) -> anyhow::Result<()> {
        buffer.reserve(length as usize);
        unsafe { buffer.set_len(length as usize); }
        
        // Read directly into the MaybeUninit buffer
        let buffer_slice = unsafe {
            std::slice::from_raw_parts_mut(buffer.as_mut_ptr() as *mut u8, length as usize)
        };
        stream.read_exact(buffer_slice)?;
        
        let fd = self.device.as_raw_fd();
        let bytes_written = unsafe {
            libc::pwrite(fd, buffer.as_ptr() as *const libc::c_void, length as usize, offset as libc::off_t)
        };
        
        let error = if bytes_written == -1 {
            let errno = std::io::Error::last_os_error().raw_os_error().unwrap_or(0) as u32;
            eprintln!("Write error at offset {}, length {}: {} (errno {})", 
                     offset, length, std::io::Error::last_os_error(), errno);
            errno
        } else if bytes_written as u32 != length {
            eprintln!("Partial write at offset {}: requested {}, got {}", 
                     offset, length, bytes_written);
            libc::EIO as u32
        } else {
            0u32
        };
        
        let reply = NbdSimpleReply {
            magic: NBD_SIMPLE_REPLY_MAGIC.into(),
            error: error.into(),
            cookie,
        };
        
        stream.write_all(bytemuck::bytes_of(&reply))?;
        
        Ok(())
    }
    
    fn handle_flush(&self, stream: &mut TcpStream, cookie: BeU64) -> anyhow::Result<()> {
        let fd = self.device.as_raw_fd();
        
        // Use DKIOCSYNCHRONIZE for block devices on macOS
        let result = unsafe { libc::ioctl(fd, DKIOCSYNCHRONIZE) };
        let error = if result == -1 {
            let errno = std::io::Error::last_os_error().raw_os_error().unwrap_or(0) as u32;
            eprintln!("Flush failed with DKIOCSYNCHRONIZE: {} (errno {})", 
                     std::io::Error::last_os_error(), errno);
            errno
        } else {
            0u32
        };
        
        let reply = NbdSimpleReply {
            magic: NBD_SIMPLE_REPLY_MAGIC.into(),
            error: error.into(),
            cookie,
        };
        
        stream.write_all(bytemuck::bytes_of(&reply))?;
        
        Ok(())
    }
}

fn main() -> anyhow::Result<()> {
    let args: Vec<String> = env::args().collect();
    if args.len() != 2 {
        eprintln!("Usage: {} <block_device_path>", args[0]);
        std::process::exit(1);
    }
    
    let server = NbdServer::new(args[1].clone())?;
    server.run()
}