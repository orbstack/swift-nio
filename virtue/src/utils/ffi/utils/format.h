#pragma once

#include <string.h>
#include <stdint.h>

#include "for_each.h"

// === Core === //

// Types
typedef void (*aprintf_writer_t)(void *self, char *start, char *end_excl);

static inline void aprintf_writer_write_range(aprintf_writer_t *writer, char *start, char *end_excl) {
    (*writer)(writer, start, end_excl);
}

static inline void aprintf_writer_write_lit(aprintf_writer_t *writer, char *text) {
    aprintf_writer_write_range(writer, text, text + strlen(text));
}

static inline void aprintf_writer_write_char(aprintf_writer_t *writer, char text) {
    char arr[2] = { text, '\0' };
    aprintf_writer_write_range(writer, &arr[0], &arr[1]);
}

typedef void (*aprintf_fmt_t)(void *self, aprintf_writer_t *writer);

static inline void aprintf_fmt(aprintf_fmt_t *fmt, aprintf_writer_t *writer) {
    (*fmt)(fmt, writer);
}

// Functions
void _orb_aprintf(aprintf_writer_t *writer, char *format, int fmt_argc, aprintf_fmt_t **fmt_argv);

static inline void aprintf(aprintf_writer_t *writer, char *format, int fmt_argc, aprintf_fmt_t **fmt_argv) {
    _orb_aprintf(writer, format, fmt_argc, fmt_argv);
}

// === Fmt Adapters === //

typedef struct aprintf_fmt_number {
    aprintf_fmt_t fmt;
    char *alphabet;
    uint64_t number;
    bool is_signed;
} aprintf_fmt_number_t;

void _orb_aprintf_fmt_number_fmt(void *self, aprintf_writer_t *writer);

static inline aprintf_fmt_number_t aprintf_fmt_number(char *alphabet, uint64_t number, bool is_signed) {
    return (aprintf_fmt_number_t) {
        .fmt = _orb_aprintf_fmt_number_fmt,
        .alphabet = alphabet,
        .number = number,
        .is_signed = is_signed
    };
}

static inline aprintf_fmt_number_t aprintf_fmt_dec(uint64_t number, bool is_signed) {
    return aprintf_fmt_number("0123456789", number, is_signed);
}

static inline aprintf_fmt_number_t aprintf_fmt_hex_lower(uint64_t number, bool is_signed) {
    return aprintf_fmt_number("0123456789abcdef", number, is_signed);
}

static inline aprintf_fmt_number_t aprintf_fmt_hex_upper(uint64_t number, bool is_signed) {
    return aprintf_fmt_number("0123456789ABCDEF", number, is_signed);
}

static inline aprintf_fmt_number_t aprintf_fmt_ptr(void *ptr) {
    return aprintf_fmt_hex_upper((uint64_t) ptr, false);
}

static inline aprintf_fmt_number_t aprintf_fmt_number_bin(uint64_t number, bool is_signed) {
    return aprintf_fmt_number("01", number, is_signed);
}

typedef struct aprintf_fmt_str {
    aprintf_fmt_t fmt;
    char *str;
} aprintf_fmt_str_t;

void _orb_aprintf_fmt_str_fmt(void *self, aprintf_writer_t *writer);

static inline aprintf_fmt_str_t aprintf_fmt_str(char *str) {
    return (aprintf_fmt_str_t) {
        .fmt = _orb_aprintf_fmt_str_fmt,
        .str = str
    };
}

// === Writer Adapters === //

typedef struct aprintf_writer_fd {
    aprintf_writer_t writer;
    int fd;
} aprintf_writer_fd_t;

void _orb_aprintf_writer_fd_writer(void *self, char *start, char *end_excl);

static inline aprintf_writer_fd_t aprintf_writer_fd(int fd) {
    return (aprintf_writer_fd_t) {
        .writer = _orb_aprintf_writer_fd_writer,
        .fd = fd
    };
}

static inline aprintf_writer_fd_t aprintf_writer_stdout() {
    return aprintf_writer_fd(1);
}

static inline aprintf_writer_fd_t aprintf_writer_stderr() {
    return aprintf_writer_fd(2);
}

// === `APRINTF` Macro === //

#define _APRINTF_VA_COUNT_HELPER(UNIQUE, IGNORED) 1 +
#define _APRINTF_BIND_FORMAT_VAR_HELPER(UNIQUE, VAR)                                               \
    __auto_type __aprintf_fmt_var_ ## UNIQUE = VAR;                                                \
    __aprintf_fmt_argv[__aprintf_fmt_argc++] = &(__aprintf_fmt_var_ ## UNIQUE).fmt;                \

#define APRINTF(TARGET, FORMAT, ...)                                                               \
    do {                                                                                           \
        /* Bind `TARGET` to a variable. */                                                         \
        __auto_type __aprintf_writer = TARGET;                                                     \
        aprintf_writer_t *__aprintf_writer_p = &__aprintf_writer.writer;                           \
                                                                                                   \
        /* Bind `FORMAT` to a variable. */                                                         \
        char *__aprintf_format = FORMAT;                                                           \
                                                                                                   \
        /* Bind each format argument to a variable. */                                             \
        aprintf_fmt_t *__aprintf_fmt_argv[FOR_EACH(_APRINTF_VA_COUNT_HELPER, __VA_ARGS__) 0];      \
        int __aprintf_fmt_argc = 0;                                                                \
        FOR_EACH(_APRINTF_BIND_FORMAT_VAR_HELPER, __VA_ARGS__) /* (macro eats semicolon) */        \
                                                                                                   \
        /* Do the print! */                                                                        \
        aprintf(__aprintf_writer_p, __aprintf_format, __aprintf_fmt_argc, __aprintf_fmt_argv);     \
    } while(0)

