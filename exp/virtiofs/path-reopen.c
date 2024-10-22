#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <fcntl.h>
#include <errno.h>

int main(int argc, char **argv) {
    int fd = open("a", O_RDWR|O_CREAT, 0644);
    if (fd == -1) {
        perror("open");
        return 1;
    }

    // hard link
    int ret = link("a", "b");
    if (ret == -1) {
        perror("link");
        return 1;
    }
    
    // unlink
    ret = unlink("a");
    if (ret == -1) {
        perror("unlink");
        return 1;
    }

    // reopen from proc
    char path[1024];
    snprintf(path, sizeof(path), "/proc/self/fd/%d", fd);
    int fd2 = open(path, O_RDWR, 0);
    if (fd2 == -1) {
        perror("open");
        return 1;
    }

    return 0;
}
