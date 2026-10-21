//go:build tools

// Package deps pins shared dependencies so parallel work doesn't race on go.mod.
package deps

import (
	_ "charm.land/bubbles/v2/key"
	_ "charm.land/bubbletea/v2"
	_ "charm.land/lipgloss/v2"
	_ "github.com/BurntSushi/toml"
	_ "github.com/charmbracelet/x/ansi"
	_ "golang.org/x/text/unicode/norm"
)
