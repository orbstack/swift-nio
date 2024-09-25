pub const KVM_MAX_CPUID_ENTRIES: usize = 80;
pub const KVM_CPUID_FLAG_SIGNIFCANT_INDEX: u32 = 1;

use vmm_sys_util::{
    fam::{FamStruct, FamStructWrapper},
    generate_fam_struct_impl,
};

#[derive(Debug, Default, Copy, Clone, PartialEq)]
#[allow(non_camel_case_types)]
#[cfg_attr(
    feature = "serde",
    derive(zerocopy::AsBytes, zerocopy::FromBytes, zerocopy::FromZeroes)
)]
pub struct kvm_cpuid_entry2 {
    pub function: u32,
    pub index: u32,
    pub flags: u32,
    pub eax: u32,
    pub ebx: u32,
    pub ecx: u32,
    pub edx: u32,
    pub padding: [u32; 3usize],
}

#[repr(transparent)]
#[derive(Default)]
#[cfg_attr(
    feature = "serde",
    derive(zerocopy::AsBytes, zerocopy::FromBytes, zerocopy::FromZeroes)
)]
pub struct __IncompleteArrayField<T>(::std::marker::PhantomData<T>, [T; 0]);
impl<T> __IncompleteArrayField<T> {
    #[inline]
    pub const fn new() -> Self {
        __IncompleteArrayField(::std::marker::PhantomData, [])
    }
    #[inline]
    pub fn as_ptr(&self) -> *const T {
        self as *const _ as *const T
    }
    #[inline]
    pub fn as_mut_ptr(&mut self) -> *mut T {
        self as *mut _ as *mut T
    }
    #[inline]
    pub unsafe fn as_slice(&self, len: usize) -> &[T] {
        ::std::slice::from_raw_parts(self.as_ptr(), len)
    }
    #[inline]
    pub unsafe fn as_mut_slice(&mut self, len: usize) -> &mut [T] {
        ::std::slice::from_raw_parts_mut(self.as_mut_ptr(), len)
    }
}
impl<T> ::std::fmt::Debug for __IncompleteArrayField<T> {
    fn fmt(&self, fmt: &mut ::std::fmt::Formatter<'_>) -> ::std::fmt::Result {
        fmt.write_str("__IncompleteArrayField")
    }
}

// Implement the FamStruct trait for kvm_cpuid2.
generate_fam_struct_impl!(
    kvm_cpuid2,
    kvm_cpuid_entry2,
    entries,
    u32,
    nent,
    KVM_MAX_CPUID_ENTRIES
);

// Implement the PartialEq trait for kvm_cpuid2.
//
// Note:
// This PartialEq implementation should not be used directly, instead FamStructWrapper
// should be used. FamStructWrapper<T> provides us with an PartialEq implementation,
// and it will determine the entire contents of the entries array. But requires
// type T to implement `Default + FamStruct + PartialEq`, so we implement PartialEq here
// and only need to determine the header field.
impl PartialEq for kvm_cpuid2 {
    fn eq(&self, other: &kvm_cpuid2) -> bool {
        // No need to call entries's eq, FamStructWrapper's PartialEq will do it for you
        self.nent == other.nent && self.padding == other.padding
    }
}

/// Wrapper over the `kvm_cpuid2` structure.
///
/// The `kvm_cpuid2` structure contains a flexible array member. For details check the
/// [KVM API](https://www.kernel.org/doc/Documentation/virtual/kvm/api.txt)
/// documentation on `kvm_cpuid2`. To provide safe access to
/// the array elements, this type is implemented using
/// [FamStructWrapper](../vmm_sys_util/fam/struct.FamStructWrapper.html).
pub type CpuId = FamStructWrapper<kvm_cpuid2>;

#[derive(Debug, Default)]
#[cfg_attr(
    feature = "serde",
    derive(zerocopy::AsBytes, zerocopy::FromBytes, zerocopy::FromZeroes)
)]
#[allow(non_camel_case_types)]
pub struct kvm_cpuid2 {
    pub nent: u32,
    pub padding: u32,
    pub entries: __IncompleteArrayField<kvm_cpuid_entry2>,
}
