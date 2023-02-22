#include <unistd.h>
#include <stdlib.h>
#include <stdio.h>
#include <sys/mount.h>

/*
equivalent to:
    cd $1
    mount --move . /
    chroot .
    exec /sbin/init "$@"
*/
int main(int argc, char** argv) {
    char* new_root = argv[1];
    char* init = argv[2];

    if (chdir(new_root) < 0) {
        perror("chdir");
        return 1;
    }

    if (mount(".", "/", NULL, MS_MOVE, NULL) < 0) {
        perror("mount");
        return 1;
    }

    if (chroot(".") < 0) {
        perror("chroot");
        return 1;
    }

    // args: init, then the rest of argv
    char** args = malloc(sizeof(char*) * (argc - 1));
    args[0] = init;
    for (int i = 2; i < argc; i++) {
        args[i - 1] = argv[i];
    }
    args[argc - 1] = NULL;

    if (execv(init, args) < 0) {
        perror("execv");
        return 1;
    }
    
    return 0;
}
