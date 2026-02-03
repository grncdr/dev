package cli

import "os"

// rememberCWD stores a stable working directory for the test and restores it on cleanup.
// If the current directory no longer exists, it recovers by switching to "/" first.
func rememberCWD() string {
	wd, err := os.Getwd()
	if err == nil {
		return wd
	}
	_ = os.Chdir("/")
	return "/"
}
