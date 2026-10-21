// Package config owns encore's file locations, the TOML config file and
// secrets (macOS Keychain with a private-file fallback).
package config

import (
	"os"
	"path/filepath"
)

const appName = "encore"

func home() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	return os.TempDir()
}

// CacheDir is ~/Library/Caches/encore: library snapshot, art, lyrics, bridge binary.
func CacheDir() string { return filepath.Join(home(), "Library", "Caches", appName) }

// StateDir is ~/Library/Application Support/encore: scrobble queue, lyric
// offsets, UI state, secret fallback.
func StateDir() string { return filepath.Join(home(), "Library", "Application Support", appName) }

// LogDir is ~/Library/Logs/encore.
func LogDir() string { return filepath.Join(home(), "Library", "Logs", appName) }

// ConfigDir is $XDG_CONFIG_HOME/encore, or ~/.config/encore.
func ConfigDir() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" && filepath.IsAbs(x) {
		return filepath.Join(x, appName)
	}
	return filepath.Join(home(), ".config", appName)
}

// ConfigPath is the config file, ConfigDir()/config.toml.
func ConfigPath() string { return filepath.Join(ConfigDir(), "config.toml") }

// EnsureDir creates dir and its parents with mode 0700 if missing.
func EnsureDir(dir string) error { return os.MkdirAll(dir, 0o700) }

// EnsureCacheDir returns CacheDir()/sub..., creating it if needed.
func EnsureCacheDir(sub ...string) (string, error) {
	return ensure(CacheDir(), sub)
}

// EnsureStateDir returns StateDir()/sub..., creating it if needed.
func EnsureStateDir(sub ...string) (string, error) {
	return ensure(StateDir(), sub)
}

// EnsureLogDir returns LogDir(), creating it if needed.
func EnsureLogDir() (string, error) { return ensure(LogDir(), nil) }

func ensure(base string, sub []string) (string, error) {
	dir := filepath.Join(append([]string{base}, sub...)...)
	return dir, EnsureDir(dir)
}
