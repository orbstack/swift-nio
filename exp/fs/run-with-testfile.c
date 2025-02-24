#include <fcntl.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <sys/uio.h>
#include <sys/ioctl.h>

int main(int argc, char **argv) {
    int fd = open(argv[1], O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd == -1) {
        perror("open");
        return 1;
    }

    ioctl(fd, 2133, 0);

    dup2(fd, STDOUT_FILENO);
    int ret = execvp(argv[2], argv + 2);
    if (ret == -1) {
        perror("execv");
        return 1;
    }
    return 0;
}
