#define _GNU_SOURCE
#define _DARWIN_C_SOURCE
#include <stdio.h>
#include <fcntl.h>
#include <unistd.h>
#include <errno.h>
#include <sys/ioctl.h>

int main(int argc, char **argv) {
    int fd = open(argv[1], O_RDONLY);
    if (fd == -1) {
        perror("open");
        return 1;
    }

    uint64_t total_sparse_chunks = 0;
    uint64_t total_allocated_regions = 0;

    // find data and holes
    off_t chunk_off = 0;
    while (1) {
        chunk_off = lseek(fd, chunk_off, SEEK_DATA);
        if (chunk_off == -1) {
            if (errno == ENXIO) {
                break;
            }
            perror("lseek");
            return 1;
        }
        off_t hole_off = lseek(fd, chunk_off, SEEK_HOLE);
        if (hole_off == -1) {
            perror("lseek");
            return 1;
        }
        off_t chunk_len = hole_off - chunk_off;

        // process this chunk:
        // for (off_t pos = chunk_off; pos < chunk_off + chunk_len; pos += 4096) {
        off_t pos = chunk_off;
        while (pos < chunk_off + chunk_len) {
            struct log2phys l2p = {
                .l2p_contigbytes = INT64_MAX,
                .l2p_devoffset = pos,
            };
            int ret = fcntl(fd, F_LOG2PHYS_EXT, &l2p);
            if (ret == -1) {
                perror("fcntl");
                return 1;
            }
            printf("%lld,%lld\n", pos, l2p.l2p_contigbytes);
            // fprintf(stderr, "pos=%lld, devoffset=%lld, contigbytes=%lld\n", pos, l2p.l2p_devoffset, l2p.l2p_contigbytes);
            // int ret = lseek(fd, pos, SEEK_DATA);
            // if (ret == -1) {
            //     perror("lseek");
            //     return 1;
            // }
            // int phys = fcntl(fd, F_LOG2PHYS, NULL);
            // if (phys == -1) {
            //     perror("fcntl");
            //     return 1;
            // }
            // printf("pos: %lld, phys: %lld\n", pos, phys);
            pos += l2p.l2p_contigbytes;
            total_allocated_regions += 1;
        }

        // printf("chunk_off: %lld, hole_off: %lld\n", chunk_off, hole_off);
        // printf("%lld %lld\n", chunk_off, chunk_len);
        chunk_off = hole_off;
        total_sparse_chunks += 1;
    }

    fprintf(stderr, "\n\ntotal sparse chunks: %lld\ntotal allocated regions: %lld\n", total_sparse_chunks, total_allocated_regions);

    return 0;
}
