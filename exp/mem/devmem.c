#include <sys/mman.h>
#include <fcntl.h>
#include <unistd.h>
#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>

int main(int argc, char **argv) {
    char *out_file = argv[1];
    if (out_file == NULL) {
        fprintf(stderr, "Usage: %s <out_file> <length>\n", argv[0]);
        return 1;
    }

    uint64_t out_len = strtoull(argv[2], NULL, 0);

    int fd = open("/dev/mem", O_RDWR | O_CLOEXEC);
    if (fd < 0) {
        perror("open");
        return 1;
    }

    void *map = mmap(NULL, 0x1000000000ULL, PROT_READ | PROT_WRITE, MAP_SHARED, fd, 0x1000000000ULL);
    if (map == MAP_FAILED) {
        perror("mmap");
        return 1;
    }

    volatile void *mapping = (volatile void *)map;
    int ofd = open(out_file, O_WRONLY | O_CREAT | O_TRUNC, 0644);
    if (ofd < 0) {
        perror("open");
        return 1;
    }

    uint64_t rem = out_len;
    while (rem > 0) {
        ssize_t ret = write(ofd, mapping, rem);
        if (ret < 0) {
            perror("write");
            return 1;
        }
        rem -= ret;
    }

    return 0;
}
