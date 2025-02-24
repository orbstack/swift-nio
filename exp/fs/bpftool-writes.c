#include <fcntl.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <sys/uio.h>
#include <sys/ioctl.h>
static void writev_line(int fd, char *str) {
    struct iovec iovs[2];
    iovs[0].iov_base = str;
    iovs[0].iov_len = strlen(str);
    iovs[1].iov_base = "\n";
    iovs[1].iov_len = 1;
    int ret = writev(fd, iovs, 2);
    if (ret == -1) {
        perror("writev");
        exit(1);
    }
}

static char *payload[] = {
#include "bpftool-payload.h"
};

static void do_payload(int fd) {
    for (int i = 0; i < sizeof(payload) / sizeof(payload[0]); i++) {
        writev_line(fd, payload[i]);
    }
}

int main(int argc, char **argv) {
    ioctl(STDOUT_FILENO, 2133, 0);
    do_payload(STDOUT_FILENO);
    return 0;
}
