use std::error::Error;

use tokio::net::UnixDatagram;
use serde::Serialize;

const REQ_BURST: u16 = 3;
const PKT_TYPE_CMD_REQUEST: u8 = 1;

const SERVER_ADDR: &str = "/var/run/chrony/chronyd.sock";

#[derive(Serialize)]
struct IPAddr {
    addr: [u8; 16],
    family: u16,
    _pad: u16,
}

#[derive(Serialize)]
struct RequestBurst {
    version: u8,
    pkt_type: u8,
    res1: u8,
    res2: u8,
    command: u16,
    attempt: u16,
    sequence: u32,
    _pad1: u32,
    _pad2: u32,

    mask: IPAddr,
    address: IPAddr,
    n_good_samples: u32,
    n_total_samples: u32,
    
    // this one doesn't need padding. request > reply len
}

pub async fn send_burst_request(n_good_samples: u32, n_total_samples: u32) -> Result<(), Box<dyn Error>> {
    // don't care because we never read reply
    let seq = 1;
    let request = RequestBurst {
        version: 6,
        pkt_type: PKT_TYPE_CMD_REQUEST,
        res1: 0,
        res2: 0,
        command: REQ_BURST,
        attempt: 0,
        sequence: seq,
        _pad1: 0,
        _pad2: 0,

        // unspecified
        mask: IPAddr {
            addr: [0; 16],
            family: 0,
            _pad: 0,
        },
        // unspecified
        address: IPAddr {
            addr: [0; 16],
            family: 0,
            _pad: 0,
        },

        n_good_samples,
        n_total_samples,
    };
    let buf = bincode::serialize(&request)?;
    
    // send to unixgram
    // don't bother to read reply
    let client_sock = UnixDatagram::unbound()?;
    client_sock.send_to(&buf, SERVER_ADDR).await?;

    Ok(())
}
