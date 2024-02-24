use vm_memory::ByteValued;

#[allow(warnings, unused)]
pub mod androidfuse;
#[allow(warnings, unused)]
pub mod fuse;


unsafe impl ByteValued for fuse::fuse_entry_out {}
unsafe impl ByteValued for androidfuse::fuse_entry_bpf_out {}
