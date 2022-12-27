#include <sys/socket.h>
#include <sys/un.h>
#include <linux/vm_sockets.h>
//#include <sys/vsock.h>
#include <errno.h>
#include <stdio.h>
#include <thread>
#include <unistd.h>

#define PING_BUFFER_SIZE 64
#define BULK_BUFFER_SIZE (1024*1024)
#define PING_ITERS 1000

int main() {
    // listen on vsock cid any port 5200
    int vsock_fd = socket(AF_VSOCK, SOCK_STREAM, 0);
    if (vsock_fd < 0) {
        perror("socket");
        return 1;
    }

    struct sockaddr_vm addr = {
        .svm_family = AF_VSOCK,
        .svm_port = 5200,
        .svm_cid = VMADDR_CID_ANY,
    };
    if (bind(vsock_fd, (struct sockaddr *)&addr, sizeof(addr)) < 0) {
        perror("bind");
        return 1;
    }

    if (listen(vsock_fd, 1) < 0) {
        perror("listen");
        return 1;
    }

    while (true) {
        uint64_t total = 0;

        // accept connection
        int vsock_conn_fd = accept(vsock_fd, NULL, NULL);
        if (vsock_conn_fd < 0) {
            perror("accept");
            return 1;
        }

        // ping
        char ping_buf[PING_BUFFER_SIZE];
        for (int i = 0; i < PING_ITERS; i++) {
            int n = read(vsock_conn_fd, ping_buf, sizeof(ping_buf));
            if (n != sizeof(ping_buf)) {
                perror("read");
                return 1;
            }

            n = write(vsock_conn_fd, ping_buf, sizeof(ping_buf));
            if (n != sizeof(ping_buf)) {
                perror("write");
                return 1;
            }
        }

        // read all data
        char buf[BULK_BUFFER_SIZE];
        while (true) {
            int n = read(vsock_conn_fd, buf, sizeof(buf));
            if (n <= 0) {
                perror("read");
                return 1;
            }
            if (n == 1 && buf[0] == 0x42) {
                break;
            }
        }

        // flip
        char flip_buf[1] = {0x42};
        int n = write(vsock_conn_fd, flip_buf, sizeof(flip_buf));
        if (n != sizeof(flip_buf)) {
            perror("write");
            return 1;
        }

        // send all data
        memset(buf, 0xda, sizeof(buf));
        while (true) {
            int n = write(vsock_conn_fd, buf, sizeof(buf));
            if (n <= 0) {
                perror("write");
                return 1;
            }
        }
    }
    return 0;
}
