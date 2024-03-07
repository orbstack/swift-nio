#include <stdio.h>
#include <unistd.h>
#include <sys/socket.h>
#include <sys/un.h>
#include <errno.h>
#include <string.h>
#include <stddef.h>
#include <stdint.h>
#include <sys/uio.h>

// very simple/flaky runc arg parser:
// for --* that contains =, don't skip
// for --* that doesn't contain =, skip next arg, unless next arg also starts with --
// save first and second (if any) positional args
void parse_args(int argc, char** argv, char** pfirst, char** psecond) {
    *pfirst = NULL;
    *psecond = NULL;

    for (int i = 1; i < argc; i++) {
        // starts with --?
        char *arg = argv[i];
        // 3: -, -, and some other char (or =)
        if (strlen(arg) >= 3 && arg[0] == '-' && arg[1] == '-') {
            // contains =?
            if (strchr(arg, '=') != NULL) {
                // contains =. that means KV pair, so skip this one, but not next
                continue;
            } else {
                // doesn't contain =. that means next arg is probably a value
                // skip next, if it's not past the end, and it's not a --
                if (i+1 < argc && argv[i+1][0] != '-') {
                    i++;
                }
                continue;
            }
        } else {
            // not --, and not a value that we skipped past, so it's a positional arg
            if (*pfirst == NULL) {
                *pfirst = arg;
            } else {
                *psecond = arg;
                break;
            }
        }
    }
}

// stub program that connects to a unix socket, waits for the conn to be closed, and then execs arguments
int main(int argc, char** argv) {
    // parse args:
    char *runc_command = NULL;
    char *cid = NULL;
    parse_args(argc, argv, &runc_command, &cid);
    
    // bail out if not command="start", arg2=<64-char container ID>
    if (runc_command != NULL &&
            strcmp(runc_command, "start") == 0 &&
            cid != NULL &&
            strlen(cid) == 64) {
        int connfd = socket(AF_UNIX, SOCK_STREAM|SOCK_CLOEXEC, 0);
        if (connfd == -1) {
            perror("socket");
            return 1;
        }

        struct sockaddr_un addr = {
            .sun_family = AF_UNIX,
            .sun_path = "/run/rc.sock",
        };
        if (connect(connfd, (struct sockaddr*)&addr, sizeof(addr)) == -1) {
            perror("connect");
            return 1;
        }

        // send it to the socket
        uint32_t cid_len = strlen(cid);
        struct iovec iov[2] = {
            {.iov_base = &cid_len, .iov_len = sizeof(cid_len)},
            {.iov_base = cid, .iov_len = cid_len},
        };
        if (writev(connfd, iov, 2) == -1) {
            perror("writev");
            return 1;
        }

        // closed = read EOF
        char buf[1];
        int len = read(connfd, buf, 1);
        if (len == -1) {
            perror("read");
            return 1;
        }

        // close conn
        close(connfd);
    }

    // exec
    execv("/usr/bin/.runc", argv);
    perror("exec");
    return 1;
}
