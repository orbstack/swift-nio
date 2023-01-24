#include <unistd.h>
#include <stdlib.h>
#include <stdio.h>
#include <errno.h>

int main(int argc, char** argv, char** envp) {
    char* fd_str = argv[1];
    int fd = atoi(fd_str);
    fexecve(fd, argv + 2, envp);
    return errno;
}
