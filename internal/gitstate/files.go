package gitstate

import (
	"io/fs"
	"os"
)

type FileReader interface {
	Lstat(string) (fs.FileInfo, error)
	ReadFile(string) ([]byte, error)
	Readlink(string) (string, error)
}

type osFiles struct{}

func (osFiles) Lstat(path string) (fs.FileInfo, error) { return os.Lstat(path) }
func (osFiles) ReadFile(path string) ([]byte, error)   { return os.ReadFile(path) }
func (osFiles) Readlink(path string) (string, error)   { return os.Readlink(path) }
