#include <stdlib.h>
#include <unistd.h>

int main(int argc, char **argv) {
    daemon(1, 0);
    return execv(argv[1], argv + 1);
}
