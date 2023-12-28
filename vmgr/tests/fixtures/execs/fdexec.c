#define _GNU_SOURCE

#include <stdio.h>
#include <fcntl.h>
#include <unistd.h>
#include <linux/openat2.h>

int main() {
    // bump fd number by 1, so execfd shifts and makes /proc/self/fd ENOENT after exec
    open("/dev/null", O_RDONLY|O_CLOEXEC);

    int fd = open("/usr/bin/ls", O_RDONLY|O_CLOEXEC|O_PATH);
    printf("fd = %d\n", fd);
    char buf[256];
    sprintf(buf, "/proc/self/fd/%d", fd);
    printf("buf = %s\n", buf);
    char *argv[] = {"/nonexist", NULL};
    execve(buf, argv, environ);
}
