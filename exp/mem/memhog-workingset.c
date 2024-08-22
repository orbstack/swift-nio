#include <mach/arm/kern_return.h>
#include <mach/arm/vm_types.h>
#include <mach/mach_error.h>
#include <mach/mach_init.h>
#include <mach/mach_vm.h>
#include <mach/vm_inherit.h>
#include <mach/vm_prot.h>
#include <mach/vm_statistics.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <unistd.h>
#include <stdio.h>

int main(int argc, const char **argv) {
    int gib = atoi(argv[1]);
    size_t bytes = gib * 1024 * 1024 * 1024;

    printf("Allocating %d GiB\n", gib);
    volatile char * volatile *chunks = malloc(gib * sizeof(char *));
    if (chunks == NULL) {
        perror("malloc");
        return 1;
    }
    for (int i = 0; i < gib; i++) {
        chunks[i] = mmap(NULL, 1024 * 1024 * 1024, PROT_READ | PROT_WRITE, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
        if (chunks[i] == MAP_FAILED) {
            perror("mmap");
            return 1;
        }
    }

    printf("Filling\n");
    for (int ci = 0; ci < gib; ci++) {
        volatile char *p = (char *)chunks[ci];
        arc4random_buf((void *)p, 1024 * 1024 * 1024);
    }

    printf("Touching\n");
    int iters = 0;
    while (1) {
        printf(" * iteration %d\n", iters++);
        for (int ci = 0; ci < gib; ci++) {
            volatile char *p = (char *)chunks[ci];
            for (int i = 0; i < 1024 * 1024 * 1024; i += 16384) {
                p[i]++;
            }
        }
    }

    return 0;    
}
