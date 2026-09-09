package config

import (
	"fmt"
	"os"
	"path/filepath"
)

type Paths struct {
	GlobalConfig string `json:"global_config"`
	RepoConfig   string `json:"repo_config"`
	StateDir     string `json:"state_dir"`
}

func ResolvePaths(repoRoot string, getenv func(string) string, userHome func() (string, error)) (Paths, error) {
	home, err := userHome()
	if err != nil || home == "" {
		return Paths{}, fmt.Errorf("cannot resolve user home")
	}
	configHome := getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		configHome = filepath.Join(home, ".config")
	}
	stateHome := getenv("XDG_STATE_HOME")
	if stateHome == "" {
		stateHome = filepath.Join(home, ".local", "state")
	}
	global, err := filepath.Abs(filepath.Join(configHome, "commiter", "config.toml"))
	if err != nil {
		return Paths{}, fmt.Errorf("cannot resolve global configuration path")
	}
	state, err := filepath.Abs(filepath.Join(stateHome, "commiter"))
	if err != nil {
		return Paths{}, fmt.Errorf("cannot resolve state path")
	}
	repo := ""
	if repoRoot != "" {
		repo, err = filepath.Abs(filepath.Join(repoRoot, ".commiter.toml"))
		if err != nil {
			return Paths{}, fmt.Errorf("cannot resolve repo configuration path")
		}
	}
	return Paths{GlobalConfig: global, RepoConfig: repo, StateDir: state}, nil
}

func DefaultPaths(repoRoot string) (Paths, error) {
	return ResolvePaths(repoRoot, os.Getenv, os.UserHomeDir)
}
