package logstore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnsureSubdir creates private directories under root, rejecting symlinks
// and paths that could escape the selected storage root.
func EnsureSubdir(root, relative string) (string, error) {
	if filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(filepath.Clean(relative), ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid storage subdirectory %q", relative)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	current := root
	parts := []string{"."}
	if relative != "" && relative != "." {
		parts = append(parts, strings.Split(filepath.Clean(relative), string(filepath.Separator))...)
	}
	for _, part := range parts {
		current = filepath.Join(current, part)
		if err := os.Mkdir(current, 0o700); err != nil && !os.IsExist(err) {
			return "", err
		}
		info, err := os.Lstat(current)
		if err != nil {
			return "", err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("storage directory is not a real directory: %s", current)
		}
	}
	return current, nil
}
