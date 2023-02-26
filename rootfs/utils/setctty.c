#include <stdlib.h>
#include <unistd.h>
#include <sys/ioctl.h>
#include <linux/tty.h>

int main(int argc, char **argv) {
    int ctty_fd = argv[1][0] - '0';
    // not fatal, ignore errors
    ioctl(ctty_fd, TIOCSCTTY, 1);
    // need PATH lookup for meta.RawCommand mode
    return execvp(argv[2], argv + 2);
}
