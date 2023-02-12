#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>
#include <fcntl.h>
#include <string.h>
#include <errno.h>
#include <time.h>

#define NS_PER_SEC 1000000000ULL

int main() {
    int fd = open("test.txt", O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) {
        perror("open");
        return 1;
    }

    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    unsigned long long start = ts.tv_sec * NS_PER_SEC + ts.tv_nsec;

    int i = 0;
    int fsyncs = 0;
    while (1) {
        clock_gettime(CLOCK_MONOTONIC, &ts);
        unsigned long long now = ts.tv_sec * NS_PER_SEC + ts.tv_nsec;
        if (now - start > 5*NS_PER_SEC) {
            break;
        }

        char buf[4096];
        memset(buf, 'a' + i, sizeof(buf));
        lseek(fd, 0, SEEK_SET);
        int ret = write(fd, buf, sizeof(buf));
        if (ret < 0) {
            perror("write");
            return 1;
        }

        if (fsync(fd) < 0) {
            perror("fsync");
            return 1;
        }
        fsyncs++;
        i++;
    }

    printf("fsyncs: %d | = %d IOPS\n", fsyncs, fsyncs/5);
    return 0;
}
