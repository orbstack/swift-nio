#include <sys/socket.h>
#include <sys/un.h>
#include <linux/vm_sockets.h>
#include <errno.h>
#include <stdio.h>
#include <unistd.h>
#include <fcntl.h>

#include <stdlib.h>
#include <string.h>

int main(int argc, char **argv) {
    int vsock_server_fd = socket(AF_VSOCK, SOCK_STREAM, 0);
    if (vsock_server_fd < 0) {
        perror("socket");
        return -1;
    }

    // listen on cid any port 1024
    struct sockaddr_vm addr = {
        .svm_family = AF_VSOCK,
        .svm_port = 2049,
        .svm_cid = VMADDR_CID_ANY,
    };
    if (bind(vsock_server_fd, (struct sockaddr *)&addr, sizeof(addr)) < 0) {
        perror("bind");
		close(vsock_server_fd);
        return -1;
    }

    if (listen(vsock_server_fd, 1) < 0) {
        perror("listen");
		close(vsock_server_fd);
        return -1;
    }

    fprintf(stdout, "%d", vsock_server_fd);
    fflush(stdout);
    fclose(stdout);

	return 0;
}
