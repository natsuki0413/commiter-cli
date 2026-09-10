package gitstate

import (
	"fmt"
	"os"
	"path/filepath"
)

type repoLock struct {
	path string
	file *os.File
}

func acquireLock(commonDir string) (*repoLock, error) {
	path := filepath.Join(commonDir, "commiter.lock")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if os.IsExist(err) {
		return nil, safety("another commiter process is using this repository")
	}
	if err != nil {
		return nil, internal("cannot acquire repository lock")
	}
	if _, err := fmt.Fprintf(file, "%d\n", os.Getpid()); err != nil {
		file.Close()
		_ = os.Remove(path)
		return nil, internal("cannot initialize repository lock")
	}
	return &repoLock{path: path, file: file}, nil
}

func (lock *repoLock) Close() error {
	closeErr := lock.file.Close()
	removeErr := os.Remove(lock.path)
	if closeErr != nil || removeErr != nil {
		return internal("cannot release repository lock")
	}
	return nil
}
