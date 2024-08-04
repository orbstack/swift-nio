/// Extract the specified bits of a 64-bit integer.
/// For example, to extrace 2 bits from offset 1 (zero based) of `6u64`,
/// following expression should return 3 (`0b11`):
/// `extract_bits_64!(0b0000_0110u64, 1, 2)`
///
#[macro_export]
macro_rules! extract_bits_64 {
    // fix warning when offset=0
    ($value: expr, 0, $length: expr) => {
        $value & (!0u64 >> (64 - $length))
    };

    ($value: expr, $offset: expr, $length: expr) => {
        ($value >> $offset) & (!0u64 >> (64 - $length))
    };
}
