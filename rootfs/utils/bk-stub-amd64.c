// buildkit stub for amd64 detection
// Rosetta fails because buildkit detector chroot into empty dir, and Rosetta requires /proc/self/exe for ioctl
// so we identify amd64 detection binary in binfmt_misc and run this hack instead
//
// rosetta error: Unable to open /proc/self/exe: 2
// amd64 supported =   signal: trace/breakpoint trap
//
// https://github.com/moby/buildkit/blob/2ff0d2a2f53663aae917980fa27eada7950ff69c/util/archutil/amd64_check.go
int main() {
    // amd64 v2 (64 + 2)
    return 66;
}
