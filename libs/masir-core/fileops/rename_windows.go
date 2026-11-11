package fileops

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"time"
)

// Rename waits at most three seconds for Windows readers (including scanners)
// that temporarily deny delete sharing. It never retries permission/path errors,
// changes an ACL, or touches the bytes the caller already verified.
func Rename(source, target string) error {
	deadline := time.Now().Add(3 * time.Second)
	for {
		e := os.Rename(source, target)
		if (!errors.Is(e, windows.ERROR_SHARING_VIOLATION) && !errors.Is(e, windows.ERROR_LOCK_VIOLATION)) || !time.Now().Before(deadline) {
			return e
		}
		time.Sleep(25 * time.Millisecond)
	}
}
