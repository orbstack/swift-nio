#include <signal.h>
#include <stdatomic.h>
#include <stdbool.h>
#include <stdlib.h>

typedef bool (*signal_callback_t)(int signum, siginfo_t *info, void *uap, void *userdata);

typedef struct signal_handler {
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
} signal_handler_t;

static signal_handler_t * _Atomic _orb_signal_handler_head;

// This is locked externally.
bool orb_init_signal_multiplexer(int signum, struct sigaction old_action) {
    signal_handler_t *handler = calloc(sizeof(signal_handler_t), 1);
    if (handler == NULL)
        return false;

    handler->signum = signum;
    handler->next = atomic_load(&_orb_signal_handler_head);
    handler->is_extern_handler = true;
    handler->callback.extern_action = old_action;

    atomic_store(&_orb_signal_handler_head, handler);
    return true;
}

// This is locked externally.
bool orb_push_signal_multiplexer(int signum, signal_callback_t user_action, void *userdata) {
    signal_handler_t *handler = calloc(sizeof(signal_handler_t), 1);
    if (handler == NULL)
        return false;

    handler->signum = signum;
    handler->next = atomic_load(&_orb_signal_handler_head);
    handler->is_extern_handler = false;
    handler->callback.user_action.func = user_action;
    handler->callback.user_action.userdata = userdata;

    atomic_store(&_orb_signal_handler_head, handler);
    return true;
}

// This is written in C so that we can use `musttail`.
void orb_signal_multiplexer(int signum, siginfo_t *info, void *uap) {
    // Handle multiplexing
    struct sigaction *old_action = NULL;
    signal_handler_t *handler = atomic_load(&_orb_signal_handler_head);

    while (handler != NULL) {
        // We're only interested in descriptors pertaining to our `signum`.
        if (handler->signum != signum)
            goto next;

        // If userdata is `NULL`, we know this is the last descriptor for `signum`.
        if (handler->is_extern_handler) {
            old_action = &handler->callback.extern_action;
            break;
        }

        // Otherwise, we have another callback to process.
        void *userdata = handler->callback.user_action.userdata;
        bool absorbed_signal = handler->callback.user_action.func(signum, info, uap, userdata);
        if (absorbed_signal) {
            return;
        }

    next:
        handler = handler->next;
    }

    // Handle chaining if the signal was never absorbed.
    if (old_action == NULL) {
        return;
    }

    if (old_action->sa_handler == SIG_DFL) {
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
