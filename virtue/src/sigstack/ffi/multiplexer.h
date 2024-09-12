#pragma once

#include <signal.h>

typedef enum signal_verdict {
    SIGNAL_VERDICT_CONTINUE,
    SIGNAL_VERDICT_HANDLED,
    SIGNAL_VERDICT_FORCE_DEFAULT,
} signal_verdict_t;

typedef signal_verdict_t (*signal_callback_t)(int signum, siginfo_t *info, void *uap, void *userdata);
