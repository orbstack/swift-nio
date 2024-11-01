#include <sys/fanotify.h>
#include <stdio.h>
#include <unistd.h>
#include <fcntl.h>

int main(int argc, char **argv) {
    int fan_fd = fanotify_init(FAN_CLASS_PRE_CONTENT|FAN_CLOEXEC, O_RDONLY|O_CLOEXEC);
    if (fan_fd == -1) {
        perror("fanotify_init");
        return 1;
    }

    int ret = fanotify_mark(fan_fd, FAN_MARK_ADD, FAN_OPEN_PERM|FAN_OPEN_EXEC_PERM|FAN_ACCESS_PERM|FAN_ONDIR, AT_FDCWD, argv[1]);
    if (ret == -1) {
        perror("fanotify_mark");
        return 1;
    }

    while (1) {
        struct fanotify_event_metadata events[32];
        printf("reading...\n");
        ssize_t len = read(fan_fd, events, sizeof(events));
        if (len == -1) {
            perror("read");
            return 1;
        }

        for (int i = 0; i < len / sizeof(struct fanotify_event_metadata); i++) {
            struct fanotify_event_metadata *event = &events[i];
            printf("event: %lx\n", event->mask);

            // reply
            struct fanotify_response response = {
                .fd = event->fd,
                .response = FAN_ALLOW,
            };
            ret = write(fan_fd, &response, sizeof(response));
            if (ret == -1) {
                perror("write");
                return 1;
            }
            if (i > 0) {
                goto out;
            }

            // close(event->fd);
        }
    }
out:
    close(fan_fd);

    sleep(1000);

    return 0;
}
