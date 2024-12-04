//go:build !darwin

package util

func RunDisclaimTCC(combinedArgs ...string) (string, error) {
	cmd := makeRunCmd(combinedArgs...)
	return finishRun(cmd, combinedArgs)
}
