use utils::extract_bits_32;

pub const ARM64_INSN_SIZE: u64 = 4;
pub const ARM64_INSN_SVC_0X80: u32 = 0xd4001001;

const ARM64_INSN_HVC_OP2_LL: u32 = 0b00010;
const ARM64_INSN_HVC_OPC_HI: u32 = 0b11010100000;

pub fn is_hypercall_insn(insn: u32) -> bool {
    // https://www.scs.stanford.edu/~zyedidia/arm64/hvc.html
    // ignores immediate
    extract_bits_32!(insn, 21, 11) == ARM64_INSN_HVC_OPC_HI
        && extract_bits_32!(insn, 0, 5) == ARM64_INSN_HVC_OP2_LL
}
