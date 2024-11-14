#include "orb_sigstack.h"

#include <errno.h>
#include <signal.h>
#include <stdatomic.h>
#include <stdbool.h>
#include <stdlib.h>

struct signal_handler {
    // The `signum` to which the handler responds.
    int signum;

    // The next handler in the chain.
    struct signal_handler *next;

    // The callback for this handler.
    bool is_extern_handler;
    union {
        // The user-defined callback.
        struct {
            signal_callback_t func;
            void *userdata;
        } user_action;

        // The externally-defined `sigaction`.
        struct sigaction extern_action;
    } callback;
};

static _Atomic(struct signal_handler *) handler_head;

// This is locked externally.
bool orb_init_signal_multiplexer(int signum, struct sigaction old_action) {
    struct signal_handler *handler = calloc(sizeof(struct signal_handler), 1);
    if (handler == NULL) {
        return false;
    }

    handler->signum = signum;
    handler->next = atomic_load(&handler_head);
    handler->is_extern_handler = true;
    handler->callback.extern_action = old_action;

    atomic_store(&handler_head, handler);
    return true;
}

// This is locked externally.
bool orb_push_signal_multiplexer(int signum, signal_callback_t user_action, void *userdata) {
    struct signal_handler *handler = calloc(sizeof(struct signal_handler), 1);
    if (handler == NULL) {
        return false;
    }

    handler->signum = signum;
    handler->next = atomic_load(&handler_head);
    handler->is_extern_handler = false;
    handler->callback.user_action.func = user_action;
    handler->callback.user_action.userdata = userdata;

    atomic_store(&handler_head, handler);
    return true;
}

// This is written in C so that we can use `musttail`.
void orb_signal_multiplexer(int signum, siginfo_t *info, void *uap) {
    // Save thread state

    // From errno's manpage: "errno is defined by the ISO C standard to be a modifiable lvalue of
    // type `int`"
    int old_errno = errno;

    // Handle multiplexing
    struct sigaction *old_action = NULL;
    struct signal_handler *handler = atomic_load(&handler_head);

    bool force_default_handling = false;

    for (; handler != NULL; handler = handler->next) {
        // We're only interested in descriptors pertaining to our `signum`.
        if (handler->signum != signum) {
            continue;
        }

        // If userdata is `NULL`, we know this is the last descriptor for `signum`.
        if (handler->is_extern_handler) {
            old_action = &handler->callback.extern_action;
            break;
        }

        // If a handler has forced default handling, we're just scanning for the `extern_action`.
        if (force_default_handling) {
            // (we're just scanning)
            continue;
        }

        // Otherwise, we have another callback to process.
        void *userdata = handler->callback.user_action.userdata;
        signal_verdict_t verdict = handler->callback.user_action.func(signum, info, uap, userdata);

        if (verdict == SIGNAL_VERDICT_CONTINUE) {
            // (fallthrough)
        } else if (verdict == SIGNAL_VERDICT_HANDLED) {
            // Let the signal return to the issuer.
            return;
        } else if (verdict == SIGNAL_VERDICT_FORCE_DEFAULT) {
            // We can't call the fallback handler immediately since we need the `old_action`. Hence,
            // we set a flag to skip over the remaining handlers until we find the extern handler
            // descriptor.
            force_default_handling = true;
        }
    }

    // Handle chaining if the signal was never absorbed.
    if (old_action == NULL) {
        // We can't `abort` here since that could lead to recursively calling our multiplexing signal
        // handler if a user subscribed to `SIGABRT`. This branch cannot be taken unless something
        // terribly wrong has occurred so let's just get this process to exit as quickly as possible.
        //
        // We certainly shouldn't just ignore the signal since that could just cause the handler to
        // repeatedly re-fault and would obscure the actual issue.

        // TODO: Use `APRINTF`
        // fprintf(stderr, "malformed signal chain descriptor: missing fallback handler for signal %d\n", signum);

        _Exit(EXIT_FAILURE);
    }

    // Restore thread state
    errno = old_errno;

    // Proceed to the next handler
    if (force_default_handling || old_action->sa_handler == SIG_DFL) {
        // default handler: terminate, but forward to OS to get correct exit status

        // uninstall our signal handler
        // TODO: this is wrong if signum's default sigaction != SIG_DFL. our handler won't run again. doesn't matter for SIGBUS
        struct sigaction new_action = {
            .sa_handler = SIG_DFL,
            .sa_flags = SA_RESTART,
            .sa_mask = old_action->sa_mask,
        };
        sigaction(signum, &new_action, NULL);

        // unmask the signal
        sigset_t mask;
        sigemptyset(&mask);
        sigaddset(&mask, signum);
        pthread_sigmask(SIG_UNBLOCK, &mask, NULL);

        // re-raise signal
        raise(signum);
    } else if (old_action->sa_handler == SIG_IGN) {
        // ignore: do nothing
    } else {
        // must be a tail call to prevent stack overflow in case of tiny signal stack
        // passing extra arguments to an sa_handler is ok
        __attribute__((musttail)) return old_action->sa_sigaction(signum, info, uap);
    }
}
