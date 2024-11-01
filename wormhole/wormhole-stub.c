/*
 * Stub process that sleeps forever.
 *
 * This blocks forever, or until SIGTERM/SIGINT/SIGQUIT is received.
 */

#include <poll.h>
#include <signal.h>
#include <stddef.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/prctl.h>
#include <unistd.h>

void signal_handler(int sig) {
    // we could rely on EINTR + non-SA_RESTART handlers, but this is simpler
    exit(0);
}

int main(int argc, char **argv) {
    // in pid 1, SIG_DFL = SIG_IGN, so we need a signal handler in order to exit on signal
    signal(SIGTERM, signal_handler);
    signal(SIGINT, signal_handler);
    signal(SIGQUIT, signal_handler);

    if (getpid() != 1) {
        // helpful message if users see this in `ps` and try to run it
        printf("This is an internal helper process for OrbStack Debug Shell.\n"
               "Run `entrypoint` to start the container.\n");
        exit(0);
    }

    // set vanity name
    int ret = prctl(PR_SET_NAME, "(entrypoint)");
    if (ret != 0) {
        perror("prctl");
        exit(1);
    }
    strcpy(argv[0], "(run `entrypoint` to start container)");

    // partial CVE-2019-5736 mitigation (and /proc/1/exe obscurity)
    ret = prctl(PR_SET_DUMPABLE, 0);
    if (ret != 0) {
        perror("prctl");
        exit(1);
    }

    // block forever
    while (1) {
        poll(NULL, 0, -1);
    }

    return 0;
}
