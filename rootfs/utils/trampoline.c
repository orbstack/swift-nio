#include <unistd.h>
#include <stdlib.h>
#include <stdio.h>
#include <errno.h>
#include <fcntl.h>

int main(int argc, char** argv, char** envp) {
    char* fd_str = argv[1];
    int fd = atoi(fd_str);
    fcntl(fd, F_SETFD, FD_CLOEXEC);
    fexecve(fd, argv + 2, envp);
    return errno;
}
