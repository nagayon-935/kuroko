package migration

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type snapshot struct {
	hash     [32]byte
	modified time.Time
}

func openLocked(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(fd), path)
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("not a regular file: %s", path)
	}
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("file is in use; close the session before migrating %s: %w", path, err)
	}
	return f, nil
}

func hashReader(r io.Reader) ([32]byte, error) {
	h := sha256.New()
	_, err := io.Copy(h, r)
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum, err
}

func fingerprint(path string) (snapshot, error) {
	f, err := openLocked(path)
	if err != nil {
		return snapshot{}, err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil {
		return snapshot{}, err
	}
	hash, err := hashReader(f)
	if err != nil {
		return snapshot{}, err
	}
	after, err := f.Stat()
	if err != nil {
		return snapshot{}, err
	}
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return snapshot{}, fmt.Errorf("file changed while verifying: %s", path)
	}
	return snapshot{hash: hash, modified: after.ModTime()}, nil
}

func readRegular(path string) ([]byte, error) {
	f, err := openLocked(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

func copyVerified(p transfer) error {
	if data, err := fingerprint(p.destination); err == nil {
		if data.hash != p.targetHash {
			return fmt.Errorf("destination changed: %s", p.destination)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	src, err := openLocked(p.source)
	if err != nil {
		return err
	}
	defer src.Close()
	var input io.Reader = src
	if p.data != nil {
		input = bytes.NewReader(p.data)
	}
	tmp, err := os.CreateTemp(filepath.Dir(p.destination), ".kuroko-migrate-*")
	if err != nil {
		return err
	}
	defer tmp.Close()
	defer os.Remove(tmp.Name())
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), input); err != nil {
		return err
	}
	if !bytes.Equal(h.Sum(nil), p.targetHash[:]) {
		return fmt.Errorf("copy checksum mismatch: %s", p.source)
	}
	if err := os.Chtimes(tmp.Name(), p.modified, p.modified); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Link(tmp.Name(), p.destination); err != nil {
		return err
	}
	return syncDir(filepath.Dir(p.destination))
}

func verify(p transfer) error {
	source, err := fingerprint(p.source)
	if err != nil {
		return err
	}
	if source.hash != p.sourceHash {
		return fmt.Errorf("source changed: %s", p.source)
	}
	target, err := fingerprint(p.destination)
	if err != nil {
		return err
	}
	if target.hash != p.targetHash {
		return fmt.Errorf("destination verification failed: %s", p.destination)
	}
	return nil
}

func removeVerified(p transfer) error {
	src, err := openLocked(p.source)
	if err != nil {
		return err
	}
	defer src.Close()
	sum, err := hashReader(src)
	if err != nil {
		return err
	}
	if sum != p.sourceHash {
		return fmt.Errorf("source changed before removal; retained %s", p.source)
	}
	target, err := fingerprint(p.destination)
	if err != nil {
		return err
	}
	if target.hash != p.targetHash {
		return fmt.Errorf("destination changed before removal; retained %s", p.source)
	}
	opened, err := src.Stat()
	if err != nil {
		return err
	}
	current, err := os.Lstat(p.source)
	if err != nil {
		return err
	}
	if !os.SameFile(opened, current) {
		return fmt.Errorf("source replaced before removal; retained %s", p.source)
	}
	if err := os.Remove(p.source); err != nil {
		return err
	}
	return syncDir(filepath.Dir(p.source))
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
