package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ============================================================================
// Link types
// ============================================================================

type linkKind int

const (
	kindURL linkKind = iota // opened with the OS default handler
	kindApp                 // macOS app name (open -a "Name"); falls back to opener elsewhere
	kindCmd                 // arbitrary shell command (sh -c "...")
)

type link struct {
	name   string
	kind   linkKind
	target string
}

// ============================================================================
// Runtime config — populated by loadConfig() before the TUI starts
// ============================================================================

var (
	links           []link
	weatherLocation string
	weatherUnits    string
	weatherEvery    time.Duration
	hnEvery         time.Duration
	hnCount         int
	windowForceSize bool
	windowCols      int
	windowRows      int
)

// ============================================================================
// JSON schema (what lives in config.json)
// ============================================================================

type linkConfig struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"` // "url", "app", or "cmd"
	Target string `json:"target"`
}

type fileConfig struct {
	Weather struct {
		Location       string `json:"location"`        // "" = auto-detect by IP
		Units          string `json:"units"`           // "u" imperial, "m" metric, "" default
		RefreshMinutes int    `json:"refresh_minutes"` // weather refresh interval
	} `json:"weather"`
	HackerNews struct {
		RefreshMinutes int `json:"refresh_minutes"`
		Count          int `json:"count"` // how many top stories to show (1-30)
	} `json:"hackernews"`
	Window struct {
		// ForceSize asks the terminal to resize to Cols x Rows at launch via
		// the CSI 8 t escape sequence. Best-effort: some terminals ignore it,
		// and it has no effect inside tmux. You can still resize freely after.
		ForceSize bool `json:"force_size"`
		Cols      int  `json:"cols"`
		Rows      int  `json:"rows"`
	} `json:"window"`
	Links []linkConfig `json:"links"`
}

func defaultFileConfig() fileConfig {
	var c fileConfig
	c.Weather.Location = ""
	c.Weather.Units = "u"
	c.Weather.RefreshMinutes = 15
	c.HackerNews.RefreshMinutes = 5
	c.HackerNews.Count = 10
	c.Window.ForceSize = true
	c.Window.Cols = 125
	c.Window.Rows = 35
	c.Links = []linkConfig{
		{Name: "Gmail", Kind: "url", Target: "https://mail.google.com"},
		{Name: "Calendar", Kind: "url", Target: "https://calendar.google.com"},
		{Name: "GitHub", Kind: "url", Target: "https://github.com"},
		{Name: "leblanc.sh", Kind: "url", Target: "https://leblanc.sh"},
		{Name: "localhost:3000", Kind: "url", Target: "http://localhost:3000"},
		{Name: "VS Code", Kind: "app", Target: "Visual Studio Code"},
		{Name: "Spotify", Kind: "app", Target: "Spotify"},
		{Name: "New tab", Kind: "cmd", Target: "open -a Ghostty ~"},
	}
	return c
}

// ============================================================================
// Loading
// ============================================================================

// configPath resolves where config.json lives. Precedence:
//  1. --config flag (passed in as override)
//  2. $WUP_CONFIG
//  3. $XDG_CONFIG_HOME/wup/config.json
//  4. ~/.config/wup/config.json
func configPath(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	if env := os.Getenv("WUP_CONFIG"); env != "" {
		return env, nil
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "wup", "config.json"), nil
}

// resolvedConfigPath holds the path loadConfig actually used, so the in-app
// "edit config" shortcut knows which file to open.
var resolvedConfigPath string

// loadConfig reads the config file, seeding a default one on first run if it
// doesn't exist yet. A malformed file is treated as a hard error so you notice.
func loadConfig(override string) error {
	path, err := configPath(override)
	if err != nil {
		return err
	}
	resolvedConfigPath = path

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		c := defaultFileConfig()
		if werr := writeConfig(path, c); werr != nil {
			fmt.Fprintf(os.Stderr, "wup: couldn't write default config to %s: %v\n", path, werr)
		} else {
			fmt.Fprintf(os.Stderr, "wup: wrote a starter config to %s\n", path)
		}
		applyConfig(c)
		return nil
	}
	if err != nil {
		return err
	}

	var c fileConfig
	if err := json.Unmarshal(data, &c); err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}
	applyConfig(c)
	return nil
}

func writeConfig(path string, c fileConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

// applyConfig converts a fileConfig into the runtime vars, clamping anything
// missing or out of range back to a sane default.
func applyConfig(c fileConfig) {
	weatherLocation = c.Weather.Location
	weatherUnits = c.Weather.Units

	if wm := c.Weather.RefreshMinutes; wm > 0 {
		weatherEvery = time.Duration(wm) * time.Minute
	} else {
		weatherEvery = 15 * time.Minute
	}

	if hm := c.HackerNews.RefreshMinutes; hm > 0 {
		hnEvery = time.Duration(hm) * time.Minute
	} else {
		hnEvery = 5 * time.Minute
	}

	switch {
	case c.HackerNews.Count <= 0:
		hnCount = 10
	case c.HackerNews.Count > 30:
		hnCount = 30
	default:
		hnCount = c.HackerNews.Count
	}

	windowForceSize = c.Window.ForceSize
	if windowCols = c.Window.Cols; windowCols <= 0 {
		windowCols = 125
	}
	if windowRows = c.Window.Rows; windowRows <= 0 {
		windowRows = 35
	}

	links = links[:0]
	for _, lc := range c.Links {
		var k linkKind
		switch strings.ToLower(strings.TrimSpace(lc.Kind)) {
		case "app":
			k = kindApp
		case "cmd", "command", "shell":
			k = kindCmd
		default: // "url" or anything unrecognized
			k = kindURL
		}
		links = append(links, link{name: lc.Name, kind: k, target: lc.Target})
	}
}
