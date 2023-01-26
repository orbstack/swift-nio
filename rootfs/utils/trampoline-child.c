#include <unistd.h>
#include <stdlib.h>
#include <stdio.h>
#include <errno.h>

int main(int argc, char** argv, char** envp) {
    int pid = fork();
    if (pid == 0) {
        int ret = execvpe(argv[1], argv + 1, envp);
        if (ret == -1) {
            perror("execvpe");
        }
        return errno;
    }

    return 0;
}
