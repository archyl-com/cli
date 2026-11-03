package repo

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// LinkFile is the file at the repository root that names its Archyl project.
// It is committed, so it can come from anyone: nothing here may choose where a
// credential goes. The Guard hook reads its projectId too.
const LinkFile = ".archyl.json"

// ErrNotLinked is returned when the repository has no link file.
var ErrNotLinked = errors.New("repository is not linked to an Archyl project")

// Link is the content of the link file. It holds no secret and is meant to be
// committed.
type Link struct {
	ProjectID      string `json:"projectId"`
	ProjectName    string `json:"projectName,omitempty"`
	OrganizationID string `json:"organizationId,omitempty"`
	APIURL         string `json:"apiUrl,omitempty"`
	// Codeowners tunes the handles archyl codeowners writes.
	Codeowners *CodeownersConfig `json:"codeowners,omitempty"`
}

// CodeownersConfig maps Archyl's teams and people to the handles the git host
// knows them by.
type CodeownersConfig struct {
	// Org prefixes team handles: @<org>/<team-slug>. It defaults to the owner
	// of the git remote.
	Org string `json:"org,omitempty"`
	// Teams maps a team's slug or name to a handle, for teams named
	// differently on the git host.
	Teams map[string]string `json:"teams,omitempty"`
	// Users maps a person's email to a handle; people left out are written
	// as their email.
	Users map[string]string `json:"users,omitempty"`
}

// ReadLink reads the link file at root.
func ReadLink(root string) (*Link, error) {
	raw, err := os.ReadFile(filepath.Join(root, LinkFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotLinked
	}
	if err != nil {
		return nil, err
	}
	var link Link
	if err := json.Unmarshal(raw, &link); err != nil {
		return nil, fmt.Errorf("parse %s: %w", LinkFile, err)
	}
	if link.ProjectID == "" {
		return nil, ErrNotLinked
	}
	return &link, nil
}

// WriteLink writes the link file at root. Keys the CLI does not know are kept.
func WriteLink(root string, link *Link) error {
	path := filepath.Join(root, LinkFile)
	if err := RefuseLink(path); err != nil {
		return err
	}
	doc := map[string]any{}
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &doc)
	}

	fields, err := json.Marshal(link)
	if err != nil {
		return err
	}
	var known map[string]any
	if err := json.Unmarshal(fields, &known); err != nil {
		return err
	}
	for _, key := range []string{"projectId", "projectName", "organizationId", "apiUrl"} {
		delete(doc, key)
	}
	for key, value := range known {
		doc[key] = value
	}

	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}
