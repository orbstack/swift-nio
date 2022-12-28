package conf

import (
	"os"
	"path/filepath"
	"runtime"
)

const (
	// TODO
	appName     = "macvirt"
	appNameUser = "MacVirt"
	nfsDirName  = "Linux"
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

func DataDir() string {
	dir := ConfigDir() + "/data"
	err := os.MkdirAll(dir, 0755)
	if err != nil {
		panic(err)
	}
	return dir
}

func GetDataFile(name string) string {
	return DataDir() + "/" + name
}

func DataImage() string {
	return GetDataFile("data.img")
}

func SwapImage() string {
	return GetDataFile("swap.img")
}

func AssetsDir() string {
	ex, err := os.Executable()
	if err != nil {
		panic(err)
	}
	exPath := filepath.Dir(ex)
	return exPath + "/../assets/" + buildVariant + "/" + Arch()
}

func GetAssetFile(name string) string {
	return AssetsDir() + "/" + name
}

func DockerSocket() string {
	return ConfigDir() + "/docker.sock"
}

func Arch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "arm64"
	default:
		panic("unsupported architecture " + runtime.GOARCH)
	}
}

func AppName() string {
	return appName
}

func AppNameUser() string {
	return appNameUser
}
