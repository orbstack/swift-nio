package conf

import "os"

const (
	// TODO
	appName    = "macvirt"
	nfsDirName = "Linux"
)

func HomeDir() string {
	home, _ := os.UserHomeDir()
	return home
}

func ConfigDir() string {
	dir := HomeDir() + "/." + appName
	err := os.MkdirAll(dir, 0755)
	if err != nil {
		panic(err)
	}
	return dir
}

func GetNfsMountDir() string {
	dir := HomeDir() + "/" + nfsDirName
	err := os.MkdirAll(dir, 0755)
	if err != nil {
		panic(err)
	}
	return dir
}
