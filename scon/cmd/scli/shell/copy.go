package shell

var emptyStr = ""

func CopyFiles(container string, srcs []string, dest string) (int, error) {
	cmdArgs := []string{"cp", "-rf"}
	cmdArgs = append(cmdArgs, srcs...)
	cmdArgs = append(cmdArgs, dest)

	opts := CommandOpts{
		CombinedArgs:  cmdArgs,
		UseShell:      false,
		ExtraEnv:      nil,
		User:          "[default]",
		ContainerName: container,
		// home
		Dir: &emptyStr,
	}

	//TODO handle users, check perm
	ret, err := RunSSH(opts)
	if err != nil {
		return 0, err
	}

	return ret, nil
}
