#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <fcntl.h>
#include <errno.h>
#include <pthread.h>
#include <mach/mach.h>

typedef __darwin_mach_port_t fileport_t;
int     fileport_makeport(int, fileport_t *);
int     fileport_makefd(fileport_t);

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

    mach_port_t smugglee_port;
    ret = fileport_makeport(smugglee_fd, &smugglee_port);
    if (ret == -1) {
        perror("fileport_makeport");
        return 1;
    }
    close(smugglee_fd);

    sleep(1);
    printf("closing holder\n");

    mach_port_deallocate(mach_task_self(), smugglee_port);

    sleep(1);
    printf("exiting\n");

    return 0;
}
