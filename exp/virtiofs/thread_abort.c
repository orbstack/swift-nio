#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <mach/mach.h>
#include <mach/thread_act.h>
#include <mach/mach_time.h>
#include <sys/stat.h>
#include <pthread.h>
#include <unistd.h>
#include <fcntl.h>

struct payload {
    thread_act_t mach_thread;
    pthread_t pthread;
};

void *thread_func(void *arg) {
    usleep(500ULL * 1000);

    printf("aborting...\n");
    struct payload *payload = (struct payload *)arg;
    kern_return_t ret = thread_abort(payload->mach_thread);
    if (ret != KERN_SUCCESS) {
        printf("thread_abort: ret=%d\n", ret);
    }

    // keep signaling it
    while (1) {
        kern_return_t ret2 = thread_abort(payload->mach_thread);
        if (ret2 != KERN_SUCCESS) {
            printf("thread_abort: ret=%d\n", ret2);
        }
        int ret3 = pthread_kill(payload->pthread, SIGUSR1);
        if (ret3 != 0) {
            printf("pthread_kill: ret=%d\n", ret);
        }
    }

    printf("aborted\n");
    return NULL;
}

void sighandler(int sig) {
    printf("sighandler: sig=%d\n", sig);
}

int main(int argc, char **argv) {
    struct payload payload = {
        .mach_thread = mach_thread_self(),
        .pthread = pthread_self(),
    };

    signal(SIGUSR1, sighandler);

    pthread_t thread;
    int ret = pthread_create(&thread, NULL, thread_func, &payload);
    if (ret != 0) {
        perror("pthread_create");
        return 1;
    }

    printf("open...\n");
    int fd = open(argv[1], O_RDWR|O_CREAT, 0644);
    if (fd == -1) {
        perror("open");
        return 1;
    }

    // fsync
    printf("fsync...\n");
    ret = fsync(fd);
    if (ret != 0) {
        perror("fsync");
        return 1;
    }

    printf("done\n");
    return 0;
}
