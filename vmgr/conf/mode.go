package conf

import "os"

func TestMode() bool {
	return os.Getenv("ORB_TEST") != ""
}
