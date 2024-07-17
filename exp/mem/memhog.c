#include <stdlib.h>
#include <string.h>

int main() {
    while (1) {
        volatile char *p = malloc(1024 * 1024);
        arc4random_buf((void *)p, 1024 * 1024);
    }

    return 0;    
}
