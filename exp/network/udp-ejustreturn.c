/*
write 60 bytes from [fd07:b51a:cc66:f0::2]:33078 to [fd07:b51a:cc66:a:8000::a]:53 fd 100
*/

#include <stdio.h>
#include <string.h>
#include <sys/socket.h>
#include <netinet/in.h>
#include <arpa/inet.h>
#include <unistd.h>
#include <errno.h>
#include <stdbool.h>

int main() {
    // make a bunch of useless fds
    for (int i = 0; i < 100; i++) {
        int fd = socket(AF_INET6, SOCK_DGRAM, 0);
        if (fd == -1) {
            perror("socket");
            return 1;
        }
    }

    while (true) {
        int fd = socket(AF_INET6, SOCK_DGRAM, 0);
        if (fd == -1) {
            perror("socket");
            return 1;
        }
        
        // struct sockaddr_in6 src_addr = {
        //     .sin6_family = AF_INET6,
        //     .sin6_port = htons(33078),
        // };
        // if (inet_pton(AF_INET6, "fd07:b51a:cc66:f0::2", &src_addr.sin6_addr) != 1) {
        //     perror("inet_pton");
        //     return 1;
        // }
        // if (bind(fd, (struct sockaddr *)&src_addr, sizeof(src_addr)) == -1) {
        //     perror("bind");
        //     return 1;
        // }

        struct sockaddr_in6 addr = {
            .sin6_family = AF_INET6,
            .sin6_port = htons(53),
        };
        if (inet_pton(AF_INET6, "fd07:b51a:cc66:a:8000::a", &addr.sin6_addr) != 1) {
            perror("inet_pton");
            return 1;
        }

        if (connect(fd, (struct sockaddr *)&addr, sizeof(addr)) == -1) {
            perror("connect");
            return 1;
        }

        printf("source IP: ");
        // system("ip addr show dev eth0 | grep inet6 | grep global | awk '{print $2}'");
        getsockname(fd, (struct sockaddr *)&addr, &(socklen_t){sizeof(addr)});
        char ip[INET6_ADDRSTRLEN];
        if (inet_ntop(AF_INET6, &addr.sin6_addr, ip, sizeof(ip)) == NULL) {
            perror("inet_ntop");
            return 1;
        }
        printf("%s\n", ip);

        char buf[60];
        memset(buf, 0xaa, sizeof(buf));
        printf("send\n");
        size_t n = write(fd,   buf, sizeof(buf));
        if (n == -1) {
            perror("write");
            return 1;
        }
        if (n != sizeof(buf)) {
            fprintf(stderr, "short write: %zu\n", n);
            // return 1;
        }

        close(fd);
    }

    return 0;
}
