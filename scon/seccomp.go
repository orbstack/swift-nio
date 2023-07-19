package main

// TODO better fix for btrfs quota
const seccompPolicy = `2
denylist
ioctl errno 1 [1,3222311976,SCMP_CMP_EQ]
init_module errno 38
finit_module errno 38
delete_module errno 38
`
