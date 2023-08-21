#include <stdio.h>
#include <unistd.h>
#include <sys/socket.h>
#include <sys/un.h>
#include <errno.h>
#include <string.h>
#include <poll.h>
#include <stddef.h>
#include <stdint.h>

// stub program that sends arguments to a unix socket, reads 2-byte return value, writes it to fd3, and then waits for SIGINT/SIGTERM
int main(int argc, char** argv) {
    int outfd = 3;
    int connfd = socket(AF_UNIX, SOCK_STREAM|SOCK_CLOEXEC, 0);
    if (connfd == -1) {
        write(outfd, "1\n", 2);
        write(outfd, strerror(errno), strlen(strerror(errno)));
        return 1;
    }

    struct sockaddr_un addr = {
        .sun_family = AF_UNIX,
        .sun_path = "/run/pstub.sock",
    };
    if (connect(connfd, (struct sockaddr*)&addr, sizeof(addr)) == -1) {
        write(outfd, "1\n", 2);
        write(outfd, strerror(errno), strlen(strerror(errno)));
        return 1;
    }

    char buf[1024];
    uint32_t arglen = 0;
    for (int i = 1; i < argc; i++) {
        arglen += strlen(argv[i]) + 1;
    }
    if (arglen > sizeof(buf)) {
        write(outfd, "1\nArgument list too long", 2);
        return 1;
    }
    // send length
    if (write(connfd, &arglen, sizeof(arglen)) == -1) {
        write(outfd, "1\n", 2);
        write(outfd, strerror(errno), strlen(strerror(errno)));
        return 1;
    }
    char *p = buf;
    for (int i = 1; i < argc; i++) {
        strcpy(p, argv[i]);
        p += strlen(argv[i]) + 1;
    }
    if (write(connfd, buf, arglen) == -1) {
        write(outfd, "1\n", 2);
        write(outfd, strerror(errno), strlen(strerror(errno)));
        return 1;
    }

    char ret_buf[1024];
    int len = read(connfd, ret_buf, sizeof(ret_buf));
    if (len == -1) {
        write(outfd, "1\n", 2);
        write(outfd, strerror(errno), strlen(strerror(errno)));
        return 1;
    }
    write(outfd, ret_buf, len);
    close(outfd);
    /* leave connfd open for signaling exit */

    // just let default SIGINT/SIGTERM handler kill us
    // agent listens to our lifetime via pidfd from peercred, so we don't need to do anything
    while (1) {
        poll(NULL, 0, -1);
    }

    return 0;
}
