use vm_memory::GuestAddress;

pub trait HvcDevice: Send + Sync {
    fn call_hvc(&self, args_addr: GuestAddress) -> i64;
    fn hvc_id(&self) -> Option<usize>;
}
