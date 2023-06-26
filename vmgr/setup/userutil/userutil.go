package userutil

/*
#include <string.h>
#include <sys/types.h>
#include <unistd.h>
#include <stdlib.h>
#include <pwd.h>

char* go_userutil_get_shell() {
	int bufsize;

	if ((bufsize = sysconf(_SC_GETPW_R_SIZE_MAX)) == -1) {
		return NULL;
	}

	char buffer[bufsize];
	struct passwd pwd, *result = NULL;
	if (getpwuid_r(getuid(), &pwd, buffer, bufsize, &result) != 0 || !result) {
		return NULL;
	}

	// make a heap copy of the string
	char* shell = malloc(strlen(pwd.pw_shell) + 1);
	strcpy(shell, pwd.pw_shell);
	return shell;
}
*/
import "C"
import "unsafe"

func GetShell() (string, error) {
	shell := C.go_userutil_get_shell()
	if shell == nil {
		return "", nil
	}
	goStr := C.GoString(shell)
	C.free(unsafe.Pointer(shell))
	return goStr, nil
}
