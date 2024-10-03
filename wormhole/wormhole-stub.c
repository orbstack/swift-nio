/*
 * Generic stub process that sleeps forever. Compiled as wormhole-stub for clarity.
 *
 * This blocks forever, or until SIGTERM/SIGINT/SIGQUIT is received. Works as both pid 1 and as a normal process.
 */

#include <signal.h>
#include <poll.h>
#include <stddef.h>
#include <stdlib.h>

void signal_handler(int sig) {
    // we could rely on EINTR + non-SA_RESTART handlers, but this is simpler
    exit(0);
}

int main() {
    // if running as pid 1, SIG_DFL = SIG_IGN, so we need a signal handler in order to exit on signal
    signal(SIGTERM, signal_handler);
    signal(SIGINT, signal_handler);
    signal(SIGQUIT, signal_handler);

    // block forever
    while (1) {
        poll(NULL, 0, -1);
    }

    return 0;
}
