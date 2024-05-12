#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>
#include <sys/reboot.h>
#include <sys/mount.h>
#include <sys/ioctl.h>
#include <string.h>
#include <errno.h>
#include <fcntl.h>
#include <termios.h>

#define IOC_RINIT _IOC(_IOC_READ, 0x61, 0x22, 0x45)
// extra bytes for confusion
#define RINIT_DATA_SIZE 1024

// "rosetta"
#define STR1     "\x54\xb9\x2b\x7f\xaa\x03\x3b"
#define STR1_KEY "\x26\xd6\x58\x1a\xde\x77\x5a"

// "virtiofs"
#define STR2     "\xbc\x37\xed\x59\x1f\xa6\xb7\xeb"
#define STR2_KEY "\xca\x5e\x9f\x2d\x76\xc9\xd1\x98"

static int fatal_err(const char* msg) {
    // as vague as possible
    printf("%s=%d\n", msg, errno);
    reboot(RB_POWER_OFF);
    return 1;
}

// put terminal in raw mode to disable \n -> \r\n translation
static int set_raw_mode(int fd) {
    struct termios raw;
    if (tcgetattr(fd, &raw) < 0) {
        return fatal_err("4");
    }
    raw.c_iflag &= ~(IGNBRK|BRKINT|PARMRK|ISTRIP|INLCR|IGNCR|ICRNL|IXON);
    raw.c_oflag &= ~OPOST;
    raw.c_lflag &= ~(ECHO|ECHONL|ICANON|ISIG|IEXTEN);
    raw.c_cflag &= ~(CSIZE|PARENB);
    raw.c_cflag |= CS8;
    raw.c_cc[VMIN] = 1;
    raw.c_cc[VTIME] = 0;
    if (tcsetattr(fd, TCSANOW, &raw) < 0) {
        return fatal_err("5");
    }
    return 0;
}

int main(int argc, char** argv) {
    // put terminal in raw mode
    if (set_raw_mode(STDIN_FILENO) != 0) {
        return fatal_err("6");
    }

    // decode XOR
    char rosetta_str_buf[sizeof(STR1)];
    for (int i = 0; i < sizeof(STR1) - 1; i++) {
        rosetta_str_buf[i] = STR1[i] ^ STR1_KEY[i];
    }
    rosetta_str_buf[sizeof(STR1) - 1] = '\0';

    // decode XOR
    char virtiofs_str_buf[sizeof(STR2)];
    for (int i = 0; i < sizeof(STR2) - 1; i++) {
        virtiofs_str_buf[i] = STR2[i] ^ STR2_KEY[i];
    }
    virtiofs_str_buf[sizeof(STR2) - 1] = '\0';

    // mount rosetta virtiofs
    // "/sbin/" to dedupe with string below
    int ret = mount(rosetta_str_buf, "/sbin/", virtiofs_str_buf, MS_NOATIME|MS_NODEV|MS_NOSUID, NULL);
    if (ret == -1) {
        return fatal_err("0");
    }

    // open rosetta binary
    char path_buf[256]; // "/sbin/rosetta"
    strcpy(path_buf, "/sbin/");
    strcat(path_buf, rosetta_str_buf);
    int fd = open(path_buf, O_CLOEXEC|O_RDONLY);
    if (fd == -1) {
        return fatal_err("1");
    }

    // ioctl to read data
    // data size is constrained by _IOC_SIZE(IOC_RINIT) = 0x45
    char buf[RINIT_DATA_SIZE];
    // 0xaa for confusion
    memset(buf, 0xaa, sizeof(buf));
    ret = ioctl(fd, IOC_RINIT, buf);
    if (ret == -1) {
        return fatal_err("2");
    }

    // print the data - all of it.
    int len = sizeof(buf);
    while (len > 0) {
        ret = write(1, buf, len);
        if (ret == -1) {
            return fatal_err("3");
        }
        len -= ret;
    }

    reboot(RB_POWER_OFF);
    return 0;
}
