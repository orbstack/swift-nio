#include <errno.h>
#include <fcntl.h>
#include <poll.h>
#include <signal.h>
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/uio.h>
#include <sys/un.h>
#include <unistd.h>

#define OUT_FD 3
#define LISTEN_SOCK_FD 4

#define eintr(ret) \
    do { \
        if ((ret) == -1 && errno == EINTR) { \
            continue; \
        } \
        break; \
    } while (1)

static int g_connfd = -1;

// dockerd kills us with SIGINT to do synchronous cleanup on stop, and expects a clean exit with
// status 0
static void sigint_handler(int sig) {
    // close write side to signal EOF to agent
    // can't just exit and let kernel clean up our fds, because the real listener is in the agent;
    // relying on agent to close the listener immediately when our connfd is auto-closed is racy
    int ret = shutdown(g_connfd, SHUT_WR);
    if (ret == -1) {
        _Exit(1);
    }

    // wait for EOF
    char buf[1];
    eintr(read(g_connfd, buf, sizeof(buf)));

    // exit with success
    _Exit(0);
}

// stub program that sends arguments to a unix socket, reads 2-byte return value, writes it to fd3,
// and then waits for SIGINT/SIGTERM
int main(int argc, char **argv) {
    // if fd 4 exists, then it's probably being used as a listen sock fd. send it to the agent.
    // check this before we open any fds
    bool send_fd4 = false;
    if (fcntl(4, F_GETFD) != -1) {
        send_fd4 = true;
    }

    int connfd = socket(AF_UNIX, SOCK_STREAM | SOCK_CLOEXEC, 0);
    if (connfd == -1) {
        write(OUT_FD, "1\n", 2);
        write(OUT_FD, strerror(errno), strlen(strerror(errno)));
        return 1;
    }

    struct sockaddr_un addr = {
        .sun_family = AF_UNIX,
        .sun_path = "/run/pstub.sock",
    };
    if (connect(connfd, (struct sockaddr *)&addr, sizeof(addr)) == -1) {
        write(OUT_FD, "1\n", 2);
        write(OUT_FD, strerror(errno), strlen(strerror(errno)));
        return 1;
    }

    char buf[1024];
    uint32_t arglen = 0;
    for (int i = 1; i < argc; i++) {
        arglen += strlen(argv[i]) + 1;
    }
    if (arglen > sizeof(buf)) {
        write(OUT_FD, "1\nArgument list too long", 2);
        return 1;
    }

    // send length and fd 4 (if applicable)
    char control_buf[CMSG_SPACE(sizeof(int))];
    struct iovec iov = {
        .iov_base = &arglen,
        .iov_len = sizeof(arglen),
    };
    struct msghdr msg = {
        .msg_iov = &iov,
        .msg_iovlen = 1,
        .msg_control = control_buf,
    };

    if (send_fd4) {
        msg.msg_controllen = CMSG_SPACE(sizeof(int));

        struct cmsghdr *cmsg = CMSG_FIRSTHDR(&msg);
        cmsg->cmsg_level = SOL_SOCKET;
        cmsg->cmsg_type = SCM_RIGHTS;
        cmsg->cmsg_len = CMSG_LEN(sizeof(int));
        *(int *)CMSG_DATA(cmsg) = 4;
    }

    if (sendmsg(connfd, &msg, 0) == -1) {
        write(OUT_FD, "1\n", 2);
        write(OUT_FD, strerror(errno), strlen(strerror(errno)));
        return 1;
    }

    // close fd 4 now that it's been sent; otherwise we'll keep the listener open until we exit
    if (send_fd4) {
        close(4);
    }

    char *p = buf;
    for (int i = 1; i < argc; i++) {
        strcpy(p, argv[i]);
        p += strlen(argv[i]) + 1;
    }
    if (write(connfd, buf, arglen) == -1) {
        write(OUT_FD, "1\n", 2);
        write(OUT_FD, strerror(errno), strlen(strerror(errno)));
        return 1;
    }

    char response_buf[1024];
    int len = read(connfd, response_buf, sizeof(response_buf));
    if (len == -1) {
        write(OUT_FD, "1\n", 2);
        write(OUT_FD, strerror(errno), strlen(strerror(errno)));
        return 1;
    }

    // register SIGINT handler early to prevent race if killed now
    g_connfd = connfd;
    signal(SIGINT, sigint_handler);

    // return result to dockerd
    // EINTR handling not needed: SIGINT handler always calls _Exit()
    int ret = write(OUT_FD, response_buf, len);
    if (ret == -1) {
        // we're not supposed to be running anymore if the pipe is closed
        return 1;
    }
    close(OUT_FD); // not supposed to retry on EINTR
    /* leave connfd open for signaling exit */

    // once the proxy is started, we have a few ways to stop:
    // - SIGINT: sent by dockerd when container stops. this needs synchronous cleanup to prevent
    // races
    // - SIGTERM: sent by dockerd PDEATHSIG. this doesn't need sync cleanup
    // - any other signal: default action
    while (1) {
        pause();
    }

    return 0;
}
