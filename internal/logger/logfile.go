package logger

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// createLogFile reserves a unique name atomically. A separate Stat followed
// by a truncating OpenFile would let simultaneous sessions overwrite each
// other, and would retry forever on stat errors other than ENOENT.
func createLogFile(dir, filename string) (*os.File, error) {
	ext := filepath.Ext(filename)
	name := strings.TrimSuffix(filename, ext)
	for i := 0; ; i++ {
		candidate := filename
		if i > 0 {
			candidate = fmt.Sprintf("%s_%d%s", name, i, ext)
		}
		f, err := os.OpenFile(filepath.Join(dir, candidate), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err == nil {
			// Held until Logger.Close; migration and rotation skip open sessions.
			if lockErr := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); lockErr != nil {
				name := f.Name()
				f.Close()
				_ = os.Remove(name)
				return nil, lockErr
			}
		}
		return f, err
	}
}
