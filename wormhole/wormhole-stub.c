/*
 * Stub process that sleeps forever.
 *
 * This blocks forever, or until SIGTERM/SIGINT/SIGQUIT is received.
 */

#define _GNU_SOURCE

#include <errno.h>
#include <poll.h>
#include <signal.h>
#include <stddef.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/prctl.h>
#include <sys/socket.h>
#include <sys/syscall.h>
#include <sys/un.h>
#include <unistd.h>

#define ATTACH_TIMEOUT (5 * 60 * 1000) // 5 minutes
#define SOCKET_PATH "/dev/shm/.orb-wormhole-stub.sock"

void signal_handler(int sig) {
    // we could rely on EINTR + non-SA_RESTART handlers, but this is simpler
    _Exit(0);
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

    int lfd = socket(AF_UNIX, SOCK_STREAM | SOCK_CLOEXEC | SOCK_NONBLOCK, 0);
    if (lfd < 0) {
        perror("socket");
        exit(1);
    }

    unlink(SOCKET_PATH);
    struct sockaddr_un addr = {
        .sun_family = AF_UNIX,
        .sun_path = SOCKET_PATH,
    };
    ret = bind(lfd, (struct sockaddr *)&addr, sizeof(addr));
    if (ret < 0) {
        perror("bind");
        exit(1);
    }

    ret = listen(lfd, 1);
    if (ret < 0) {
        perror("listen");
        exit(1);
    }

    // spend up to 5 minutes waiting for an attach
    // if none is received, the client probably crashed before attaching, so exit
    // timeout handling is wrong on EINTR, but we should never get EINTR because our only signal
    // handlers just call exit()
    struct pollfd pfd = {
        .fd = lfd,
        .events = POLLIN,
    };
    ret = poll(&pfd, 1, ATTACH_TIMEOUT);
    if (ret < 0 || !(pfd.revents & POLLIN)) {
        perror("poll");
        exit(1);
    }
    int cfd = accept4(lfd, NULL, NULL, SOCK_CLOEXEC);
    if (cfd < 0) {
        perror("accept");
        exit(1);
    }
    close(lfd);
    unlink(SOCKET_PATH);

    // block until the client exits
    signal(SIGPIPE, SIG_IGN);
    while (1) {
        char buf[1];
        ret = read(cfd, buf, 1);
        if (ret == -1) {
            if (errno == EPIPE || errno == ECONNRESET) {
                break;
            }
            perror("read");
            exit(1);
        }
        if (ret == 0) {
            break;
        }
    }

    return 0;
}
