#include <stdbool.h>
#include <stdint.h>
#include <string.h>
#include <unistd.h>

#include "format.h"

// === Core === //

static inline char *cursor_read(char **cursor) {
	char *curr = *cursor;

	if (*curr != '\0')
		(*cursor)++;

	return curr;
}

void _orb_aprintf(aprintf_writer_t *writer, char *format, int fmt_argc, aprintf_fmt_t **fmt_argv) {
	char *lit_slice_start = format;

    aprintf_fmt_t **curr_fmt = fmt_argv;
    aprintf_fmt_t **last_fmt = curr_fmt + fmt_argc;

	while (true) {
		char *curr = cursor_read(&format);

		// Flush the literal if we encounter a special `%` character of if we end the format string
        // with some special characters.
		if (lit_slice_start != format && (*curr == '\0' || *curr == '%')) {
			aprintf_writer_write_range(writer, lit_slice_start, curr);
            lit_slice_start = curr + 1;
		}

        // Handle non-special characters.
        if (*curr == '\0')
            break;  // EOS

        if (*curr != '%')
            continue;  // Regular char

        // Handle `%` escape.
        if (*format == '%') {
            // Skip past the second `%`
            cursor_read(&format);

            // Write a "%" literal
            aprintf_writer_write_lit(writer, "%");
            continue;
        }

        // Otherwise, format the argument.
        if (curr_fmt == last_fmt) {
            // Not enough arguments! Write out the error!
            aprintf_writer_write_lit(writer, "[format error: not enough arguments]");
            continue;
        }

        aprintf_fmt(*curr_fmt, writer);
        curr_fmt++;
	}
}

// === Fmt Adapters === //

void _orb_aprintf_fmt_number_fmt(void *self_, aprintf_writer_t *writer) {
    aprintf_fmt_number_t *self = self_;

    uint64_t number = self->number;

    // Handle the sign
    if (self->is_signed) {
        int64_t number_signed = (int64_t) number;
        if (number_signed < 0) {
            number = (uint64_t)(-number_signed);
            aprintf_writer_write_char(writer, '-');
        }
    }

    // Determine the radix
    int radix = strlen(self->alphabet);
    if (radix < 2) {
        aprintf_writer_write_lit(writer, "[format error: radix is too small]");
    }

    // Write out the number in reversed order
    char buf[64] = {};
    char *bufc = &buf[63];

    while (number != 0) {
        *(bufc--) = self->alphabet[number % radix];
        number /= radix;
    }

    // Write out the number
    aprintf_writer_write_range(writer, bufc+1, &buf[64]);
}

void _orb_aprintf_fmt_str_fmt(void *self_, aprintf_writer_t *writer) {
    aprintf_fmt_str_t *self = self_;
    aprintf_writer_write_lit(writer, self->str);
}

// === Writer Adapters === //

void _orb_aprintf_writer_fd_writer(void *self_, char *start, char *end_excl) {
    aprintf_writer_fd_t *self = self_;

    while (start != end_excl) {
        int written = write(self->fd, start, end_excl - start);
        if (written == -1)
            break;

        start += written;
    }
}
