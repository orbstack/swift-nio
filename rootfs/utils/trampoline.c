#include <unistd.h>
#include <stdlib.h>
#include <stdio.h>
#include <errno.h>
#include <fcntl.h>
#include <sys/mount.h>
#define _GNU_SOURCE
#include <sched.h>

int main(int argc, char** argv, char** envp) {
    char* fd_str = argv[1];
    int fd = atoi(fd_str);
    fcntl(fd, F_SETFD, FD_CLOEXEC);

	int ret = unshare(CLONE_NEWNS);
	if (ret < 0) {
		perror("unshare");
        return 1;
    }

	ret = umount2("/proc", MNT_DETACH);
	if (ret < 0) {
		perror("umount2");
        return 1;
    }

	ret = mount("none", "/proc", "proc", 0, NULL);
	if (ret < 0) {
		perror("mount");
        return 1;
    }

    fexecve(fd, argv + 2, envp);
    return errno;
}
