package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/archyl-com/cli/internal/api"
)

// modelFormat names the snapshot layout. Bump it whenever ModelSnapshot or
// the API types it embeds change shape: older snapshots then live in another
// directory and are never read.
const modelFormat = "v1"

// ModelSnapshot is what a command needs to render a project's model without
// the network.
type ModelSnapshot struct {
	Project api.Project `json:"project"`
	Model   api.Model   `json:"model"`
	ADRs    []api.ADR   `json:"adrs"`
}

// Scope is whose view of the model a snapshot holds. Every field is part of
// the key, so switching profile, host, organization or API key never reads
// another tenant's snapshot.
type Scope struct {
	Profile string
	Host    string
	OrgID   string
	// Identity tells apart credentials that share a profile, such as an
	// ARCHYL_API_KEY override. It is hashed, never written as is.
	Identity string
}

// Disabled reports whether ARCHYL_NO_CACHE turns the model cache off.
func Disabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("ARCHYL_NO_CACHE")))
	return v != "" && v != "0" && v != "false"
}

// LoadModel reads a project's snapshot; a miss is a nil entry.
func LoadModel(s Scope, projectID string) (*Entry[ModelSnapshot], error) {
	return loadFile[ModelSnapshot](s.path(projectID, ".json"))
}

// SaveModel stores a project's snapshot.
func SaveModel(s Scope, projectID string, snap ModelSnapshot) error {
	return saveFile(s.path(projectID, ".json"), snap)
}

// InvalidateModel drops a project's snapshot. Commands that change the model
// call it so the next read goes to the API.
func InvalidateModel(s Scope, projectID string) error {
	err := os.Remove(s.path(projectID, ".json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Key names a small per-project value, such as the prompt state, for Load and
// Save. It lives beside the profile's snapshots, so it is scoped and cleared
// the same way.
func (s Scope) Key(kind, projectID string) string {
	rel, _ := filepath.Rel(Dir(), s.path(projectID, "-"+kind))
	return rel
}

// ClaimRefresh reports whether a background refresh of the project should
// start, and records that one did: one per window, however often shell
// completion asks.
func ClaimRefresh(s Scope, projectID string, window time.Duration) bool {
	marker := s.path(projectID, ".refresh")
	if info, err := os.Stat(marker); err == nil && time.Since(info.ModTime()) < window {
		return false
	}
	if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
		return false
	}
	return os.WriteFile(marker, nil, 0o600) == nil
}

// ClearProfile removes every snapshot taken under a profile, in every format.
func ClearProfile(profile string) error {
	dirs, _ := filepath.Glob(filepath.Join(modelRoot(), "*", digest(profile)))
	var errs []error
	for _, dir := range dirs {
		errs = append(errs, os.RemoveAll(dir))
	}
	return errors.Join(errs...)
}

// ClearModels removes every snapshot of every profile.
func ClearModels() error {
	return os.RemoveAll(modelRoot())
}

func modelRoot() string {
	return filepath.Join(Dir(), "models")
}

// path lays snapshots out as models/<format>/<profile>/<scope+project>, so
// logging out of a profile is one directory to remove.
func (s Scope) path(projectID, ext string) string {
	return filepath.Join(modelRoot(), modelFormat, digest(s.Profile), digest(s.Host, s.OrgID, s.Identity, projectID)+ext)
}

func digest(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(h[:12])
}
