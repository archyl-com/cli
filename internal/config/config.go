// Package config stores the CLI's contexts: which Archyl instance and
// organization each named profile talks to. Credentials live elsewhere (see
// package credentials); this file is safe to read and share.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// DefaultHost is Archyl Cloud's API.
const DefaultHost = "https://api.archyl.com"

// DefaultContext is the name given to the first context a login creates.
const DefaultContext = "default"

// Context is one Archyl instance and organization the CLI can talk to.
type Context struct {
	Host           string `yaml:"host"`
	AppURL         string `yaml:"app_url,omitempty"`
	OrganizationID string `yaml:"organization_id,omitempty"`
	User           string `yaml:"user,omitempty"`
}

// File is the persisted configuration.
type File struct {
	Current  string             `yaml:"current,omitempty"`
	Contexts map[string]Context `yaml:"contexts,omitempty"`

	path string
}

// Dir returns the configuration directory: $ARCHYL_CONFIG_DIR, or archyl/
// under the OS user configuration directory.
func Dir() (string, error) {
	if dir := os.Getenv("ARCHYL_CONFIG_DIR"); dir != "" {
		return dir, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate configuration directory: %w", err)
	}
	return filepath.Join(base, "archyl"), nil
}

// Load reads the configuration from dir. A missing file is an empty
// configuration, not an error.
func Load(dir string) (*File, error) {
	f := &File{Contexts: map[string]Context{}, path: filepath.Join(dir, "config.yaml")}

	raw, err := os.ReadFile(f.path)
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", f.path, err)
	}
	if err := yaml.Unmarshal(raw, f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", f.path, err)
	}
	if f.Contexts == nil {
		f.Contexts = map[string]Context{}
	}
	return f, nil
}

// Save writes the configuration back to disk.
func (f *File) Save() error {
	if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
		return fmt.Errorf("create configuration directory: %w", err)
	}
	raw, err := yaml.Marshal(f)
	if err != nil {
		return err
	}
	return os.WriteFile(f.path, raw, 0o600)
}

// Resolve picks the context to use: the explicit name if given, then the
// current one. ok is false when nothing is configured.
func (f *File) Resolve(name string) (string, Context, bool) {
	if name == "" {
		name = f.Current
	}
	ctx, ok := f.Contexts[name]
	return name, ctx, ok
}

// Names lists the configured contexts alphabetically.
func (f *File) Names() []string {
	names := make([]string, 0, len(f.Contexts))
	for name := range f.Contexts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
