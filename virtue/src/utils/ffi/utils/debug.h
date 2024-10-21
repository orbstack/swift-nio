#pragma once

#include <Mach/Mach.h>
#include <stdbool.h>
#include <stdlib.h>

#include "aprintf.h"

#define TODO(...) FATAL("not implemented")

// We use `## __VA_ARGS__` to ensure that the trailing comma is removed if `__VA_ARGS__` is empty.
// https://stackoverflow.com/a/3563226
#define INFO(MSG, ...)               \
    aprintf(                         \
        "INFO at %s:%d: " MSG "\n",  \
        __FILE__,                    \
        __LINE__,                    \
        ## __VA_ARGS__               \
    )

// ...same here!
#define WARN(MSG, ...)               \
    aprintf(                         \
        "WARN at %s:%d: " MSG "\n",  \
        __FILE__,                    \
        __LINE__,                    \
        ## __VA_ARGS__               \
    )

// ...same here!
#define FATAL(MSG, ...)                   \
    do {                                  \
        aprintf(                          \
            "FATAL at %s:%d: " MSG "\n",  \
            __FILE__,                     \
            __LINE__,                     \
            ## __VA_ARGS__                \
        );                                \
        _Exit(EXIT_FAILURE);              \
    } while(0)

#define MACH_CHECK_WARN(RES)              \
    do {                                  \
        kern_return_t res = RES;          \
        if (res != KERN_SUCCESS) {        \
            WARN("mach error: %d", res);  \
        }                                 \
    } while(0)

#define MACH_CHECK_FATAL(RES)              \
    do {                                   \
        kern_return_t res = RES;           \
        if (res != KERN_SUCCESS) {         \
            FATAL("mach error: %d", res);  \
        }                                  \
    } while(0)
