#include <poll.h>
#include <stddef.h>

int main() {
    while (1) {
        poll(NULL, 0, -1);
    }
    return 0;
}
