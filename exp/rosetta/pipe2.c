#define _GNU_SOURCE
#include <fcntl.h>
#include <unistd.h>
#include <stdio.h>

int main()
{
	int fd[2];
	printf("const = %ld\n", O_CLOEXEC|O_DIRECT);
	if (pipe2(fd, O_CLOEXEC | O_DIRECT) < 0) {
		perror("cannot create packetized pipe");
	}

	return 0;
}
