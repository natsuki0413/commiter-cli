package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const GlobalTemplate = `schema_version = 1

[commit]
language = "en"
confirm = true

[push]
enabled = true
confirm = true

[llm]
model = "qwen3.5:4b-q4_K_M"
endpoint = "http://127.0.0.1:11434"
context = "auto"
max_context_tokens = 32768

[analysis]
untracked = "auto-safe"
include = []
exclude = []

[metrics]
persist = false

[safety]
additional_sensitive_patterns = []
`

const RepoTemplate = `schema_version = 1

[commit]
language = "en"

[llm]
model = "qwen3.5:4b-q4_K_M"
context = "auto"
max_context_tokens = 32768

[analysis]
include = []
exclude = []

[verification]
autodetect = true
timeout_seconds = 600
`

func Init(path, contents string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("cannot create configuration directory")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("configuration already exists")
	}
	if err != nil {
		return fmt.Errorf("cannot create configuration")
	}
	if _, err := file.WriteString(contents); err != nil {
		_ = file.Close()
		return fmt.Errorf("cannot write configuration")
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("cannot close configuration")
	}
	return nil
}
