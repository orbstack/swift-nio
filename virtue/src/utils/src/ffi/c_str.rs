pub fn malloc_str(text: &str) -> *const libc::c_char {
    // Sanitize "nul" bytes from `text`.
    let mut text = text; // (upcasts `&'universal str` to `&'anon str`)
    let text_owned: String;

    if memchr::memchr(0, text.as_bytes()).is_some() {
        text_owned = text.replace('\0', "\\0");
        text = &text_owned;
    }

    // Copy into a new allocation with the appropriate "nul" byte.
    unsafe {
        // Cannot overflow because `abort_msg.len()` is at most `usize::MAX`.

        let str_data = libc::malloc(text.len() + 1) as *mut u8;
        if str_data.is_null() {
            panic!("out of memory");
        }
        str_data.copy_from_nonoverlapping(text.as_ptr(), text.len());
        *str_data.add(text.len()) = b'\0';

        str_data as *const libc::c_char
    }
}
