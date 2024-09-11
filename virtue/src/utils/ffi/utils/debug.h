#pragma once

#include <stdlib.h>
#include <stdio.h>
#include <Mach/Mach.h>

#define TODO(...) FATAL("not implemented")

// We use `## __VA_ARGS__` to ensure that the trailing comma is removed if `__VA_ARGS__` is empty.
// https://stackoverflow.com/a/3563226
#define INFO(MSG, ...)               \
    fprintf(                         \
        stderr,                      \
        "INFO at %s:%d: " MSG "\n",  \
        __FILE__, __LINE__,          \
        ## __VA_ARGS__               \
    )

// ...same here!
#define WARN(MSG, ...)               \
    fprintf(                         \
        stderr,                      \
        "WARN at %s:%d: " MSG "\n",  \
        __FILE__, __LINE__,          \
        ## __VA_ARGS__               \
    )

// ...same here!
#define FATAL(MSG, ...)                   \
    do {                                  \
        fprintf(                          \
            stderr,                       \
            "FATAL at %s:%d: " MSG "\n",  \
            __FILE__, __LINE__,           \
            ## __VA_ARGS__                \
        );                                \
        exit(EXIT_FAILURE);               \
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
