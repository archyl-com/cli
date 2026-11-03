package shell

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/archyl-com/cli/internal/api"
)

// keepTranscripts bounds how many conversations a repository keeps.
const keepTranscripts = 30

// Turn is one thing the session printed, kept to print it again on resume.
type Turn struct {
	// Kind is "question", "action", "answer" or "note".
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// Transcript is a conversation saved on disk. The chat API keeps no
// conversation of its own: the transcript is what resuming sends back.
type Transcript struct {
	ID           string            `json:"id"`
	Title        string            `json:"title"`
	ProjectID    string            `json:"projectId,omitempty"`
	Updated      time.Time         `json:"updated"`
	Conversation []api.ChatMessage `json:"conversation"`
	Turns        []Turn            `json:"turns"`
}

// Questions counts the questions asked.
func (t *Transcript) Questions() int {
	n := 0
	for _, turn := range t.Turns {
		if turn.Kind == "question" {
			n++
		}
	}
	return n
}

// SessionDir is where the conversations of a repository and project live.
func SessionDir(cacheDir, repoRoot, projectID string) string {
	sum := sha256.Sum256([]byte(repoRoot + "\x00" + projectID))
	return filepath.Join(cacheDir, "sessions", hex.EncodeToString(sum[:8]))
}

func newTranscript(projectID string) *Transcript {
	var suffix [3]byte
	_, _ = rand.Read(suffix[:])
	return &Transcript{
		ID:        time.Now().Format("20060102-150405") + "-" + hex.EncodeToString(suffix[:]),
		ProjectID: projectID,
	}
}

// saveTranscript writes a transcript atomically and prunes the oldest ones.
func saveTranscript(dir string, t *Transcript) error {
	if dir == "" || len(t.Conversation) == 0 {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	t.Updated = time.Now()
	raw, err := json.Marshal(t)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".transcript-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, t.ID+".json")); err != nil {
		return err
	}
	pruneTranscripts(dir)
	return nil
}

func pruneTranscripts(dir string) {
	all := listTranscripts(dir)
	for _, t := range all[min(len(all), keepTranscripts):] {
		_ = os.Remove(filepath.Join(dir, t.ID+".json"))
	}
}

// listTranscripts returns the saved conversations, most recent first.
func listTranscripts(dir string) []*Transcript {
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []*Transcript
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		t, err := loadTranscript(dir, strings.TrimSuffix(e.Name(), ".json"))
		if err == nil {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out
}

func loadTranscript(dir, id string) (*Transcript, error) {
	if strings.ContainsAny(id, `/\`) || strings.HasPrefix(id, ".") {
		return nil, os.ErrNotExist
	}
	raw, err := os.ReadFile(filepath.Join(dir, id+".json"))
	if err != nil {
		return nil, err
	}
	var t Transcript
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, err
	}
	t.ID = id
	return &t, nil
}

// ago says how long ago t was, briefly.
func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m ago"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h ago"
	default:
		return strconv.Itoa(int(d.Hours()/24)) + "d ago"
	}
}
