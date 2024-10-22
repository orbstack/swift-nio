#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <fcntl.h>
#include <errno.h>
#include <pthread.h>
#include <mach/mach.h>
#include <sys/socket.h>
#include <sys/un.h>

typedef __darwin_mach_port_t fileport_t;
int     fileport_makeport(int, fileport_t *);
int     fileport_makefd(fileport_t);

// >=255: EINVAL on sendmsg
#define NUM_FDS 254

void *monitor_eof(void *arg) {
    int fd = (int)arg;
    while (1) {
        char buf[1];
        int ret = read(fd, buf, 1);
        if (ret == 0) {
            printf("EOF\n");
            break;
        } else {
            printf("read %d\n", ret);
        }
    }
}

int main(int argc, char **argv) {
    int pfd[2];
    int ret = pipe(pfd);
    if (ret == -1) {
        perror("pipe");
        return 1;
    }

    // another thread monitors for EOF
    pthread_t tid;
    ret = pthread_create(&tid, NULL, monitor_eof, (void *)pfd[0]);
    if (ret == -1) {
        perror("pthread_create");
        return 1;
    }

    printf("smuggling\n");

    // smugglee_fd is the fd to smuggle
    int smugglee_fd = pfd[1];

    int sfds[2];
    ret = socketpair(AF_UNIX, SOCK_STREAM, 0, sfds);
    if (ret == -1) {
        perror("socketpair");
        return 1;
    }

    // SCM_RIGHTS cmsg
    struct {
        struct cmsghdr cmsg;
        int fd[NUM_FDS];
    } cmsg_data;
    cmsg_data.cmsg.cmsg_level = SOL_SOCKET;
    cmsg_data.cmsg.cmsg_type = SCM_RIGHTS;
    cmsg_data.cmsg.cmsg_len = CMSG_LEN(sizeof(int) * NUM_FDS);
    for (int i = 0; i < NUM_FDS - 1; i++) {
        // stuff it with useless fds to find the limit
        int pipe2_fds[2];
        ret = pipe(pipe2_fds);
        if (ret == -1) {
            perror("pipe");
            return 1;
        }
        cmsg_data.fd[i] = pipe2_fds[0];
    }
    cmsg_data.fd[NUM_FDS - 1] = smugglee_fd;

    struct iovec iov = { .iov_base = "", .iov_len = 0 };
    struct msghdr msg = { 0 };
    msg.msg_control = &cmsg_data;
    msg.msg_controllen = sizeof(cmsg_data);
    msg.msg_iov = &iov;
    msg.msg_iovlen = 1;

    ret = sendmsg(sfds[0], &msg, 0);
    if (ret == -1) {
        perror("sendmsg");
        return 1;
    }
    close(smugglee_fd);
    close(sfds[0]);

    sleep(1);
    printf("closing holder\n");

    close(sfds[1]);

    sleep(1);
    printf("exiting\n");

    return 0;
}
