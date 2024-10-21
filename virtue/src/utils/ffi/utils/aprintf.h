#pragma once

#include <stdarg.h>

void _orb_aprintf(char const *fmt, va_list args);

static inline void aprintf(char const *fmt, ...) {
    va_list args;
    va_start(args, fmt);
    _orb_aprintf(fmt, args);
    va_end(args);
}
