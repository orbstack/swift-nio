#pragma once

#include <Mach/Mach.h>
#include <stdbool.h>
#include <stdlib.h>

#include "format.h"

#define TODO(...) FATAL("not implemented")

// We use `## __VA_ARGS__` to ensure that the trailing comma is removed if `__VA_ARGS__` is empty.
// https://stackoverflow.com/a/3563226
#define INFO(MSG, ...)                     \
    APRINTF(                               \
        aprintf_writer_stderr(),           \
        "INFO at %:%: " MSG "\n",          \
        aprintf_fmt_str(__FILE__),         \
        aprintf_fmt_dec(__LINE__, false),  \
        ## __VA_ARGS__                     \
    )

// ...same here!
#define WARN(MSG, ...)                     \
    APRINTF(                               \
        aprintf_writer_stderr(),           \
        "WARN at %:%: " MSG "\n",          \
        aprintf_fmt_str(__FILE__),         \
        aprintf_fmt_dec(__LINE__, false),  \
        ## __VA_ARGS__                     \
    )

// ...same here!
#define FATAL(MSG, ...)                        \
    do {                                       \
        APRINTF(                               \
            aprintf_writer_stderr(),           \
            "FATAL at %:%: " MSG "\n",         \
            aprintf_fmt_str(__FILE__),         \
            aprintf_fmt_dec(__LINE__, false),  \
            ## __VA_ARGS__                     \
        );                                     \
        _Exit(EXIT_FAILURE);                   \
    } while(0)

#define MACH_CHECK_WARN(RES)                                      \
    do {                                                          \
        kern_return_t res = RES;                                  \
        if (res != KERN_SUCCESS) {                                \
            WARN("mach error: %", aprintf_fmt_dec(res, false));   \
        }                                                         \
    } while(0)

#define MACH_CHECK_FATAL(RES)                                     \
    do {                                                          \
        kern_return_t res = RES;                                  \
        if (res != KERN_SUCCESS) {                                \
            FATAL("mach error: %", aprintf_fmt_dec(res, false));  \
        }                                                         \
    } while(0)
