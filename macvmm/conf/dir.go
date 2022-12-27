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

// TODO
func AssetsDir() string {
	return "../assets/" + buildVariant
}

func GetAssetFile(name string) string {
	return AssetsDir() + "/" + name
}

func DockerSocket() string {
	return ConfigDir() + "/docker.sock"
}
