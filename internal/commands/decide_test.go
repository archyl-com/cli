package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/decide"
	"github.com/archyl-com/cli/internal/repo"
	"github.com/archyl-com/cli/internal/ui"
)

// fakeDecideRepo is a branch that adds an outbox to the Orders API.
type fakeDecideRepo struct {
	changes []repo.Change
}

func (f *fakeDecideRepo) DefaultBase(context.Context) string          { return "origin/main" }
func (f *fakeDecideRepo) Resolves(_ context.Context, ref string) bool { return ref == "origin/main" }
func (f *fakeDecideRepo) Branch(context.Context) string               { return "feat/outbox" }

func (f *fakeDecideRepo) Changes(context.Context, string) ([]repo.Change, error) {
	return f.changes, nil
}

func (f *fakeDecideRepo) Diff(context.Context, string) ([]repo.FileDiff, error) {
	var diffs []repo.FileDiff
	for _, c := range f.changes {
		diffs = append(diffs, repo.FileDiff{Path: c.Path, Text: "diff --git a/" + c.Path + " b/" + c.Path + "\n+func WriteOutbox() {}\n"})
	}
	return diffs, nil
}

// decideCall is one request the fake API received.
type decideCall struct {
	Route string
	Body  string
}

// decideAPI is a fake Archyl API for decide: locate, decisions, the chat
// stream, decision creation and links.
type decideAPI struct {
	reply       string
	failLinksTo string
	// extraTouched adds that many more touched components to the impact.
	extraTouched int

	mu    sync.Mutex
	calls []decideCall
}

func (a *decideAPI) serve(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		route := r.Method + " " + strings.TrimPrefix(r.URL.Path, "/api/v1")
		a.mu.Lock()
		a.calls = append(a.calls, decideCall{Route: route, Body: string(raw)})
		a.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch route {
		case "POST /projects/p1/harness/locate":
			extra := ""
			for i := range a.extraTouched {
				extra += fmt.Sprintf(`,{"id":"c%d","name":"Part %d","type":"component"}`, i, i)
			}
			_, _ = io.WriteString(w, `{"files":[{"path":"internal/orders/outbox.go","elements":[{"id":"api","type":"container","name":"Orders API"}]}],`+
				`"impact":{"elements":[`+
				`{"id":"api","name":"Orders API","type":"container","path":"Shop / Orders API","dependents":[{"id":"web","name":"Storefront","type":"container","distance":1}]},`+
				`{"id":"db","name":"Orders DB","type":"container","path":"Shop / Orders DB"}`+extra+`]}}`)
		case "GET /projects/p1/adrs":
			_, _ = io.WriteString(w, `{"data":[{"id":"a4","number":4,"title":"Publish order events straight to Kafka","status":"accepted"}]}`)
		case "POST /chat":
			w.Header().Set("Content-Type", "text/event-stream")
			for _, chunk := range []string{a.reply[:len(a.reply)/2], a.reply[len(a.reply)/2:]} {
				data, _ := json.Marshal(map[string]string{"content": chunk})
				_, _ = io.WriteString(w, "data: "+string(data)+"\n\n")
			}
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
		case "POST /projects/p1/adrs":
			var req map[string]any
			_ = json.Unmarshal(raw, &req)
			w.WriteHeader(http.StatusCreated)
			// The title comes back with an escape sequence, as a hostile
			// server could send it.
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "adr-12", "number": 12, "title": fmt.Sprint(req["title"], "\x1b\x07"), "status": req["status"]})
		case "POST /adrs/adr-12/links":
			if a.failLinksTo != "" && strings.Contains(string(raw), `"`+a.failLinksTo+`"`) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = io.WriteString(w, `{"error":"Failed to create link"}`)
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":"l1"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":"Not Found"}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// sent returns the bodies of the requests made to a route.
func (a *decideAPI) sent(route string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var bodies []string
	for _, c := range a.calls {
		if c.Route == route {
			bodies = append(bodies, c.Body)
		}
	}
	return bodies
}

const decideReply = "Sure:\n```json\n" + `{"title":"Publish order events through an outbox table",` +
	`"context":"Order events were sent to Kafka inside the request.",` +
	`"decision":"Write each event to an outbox table in the order's transaction.",` +
	`"consequences":{"positive":["No event is lost when Kafka is down"],"negative":["The outbox needs purging"]},` +
	`"alternatives":[{"option":"Keep publishing in the request","whyNot":"events are lost on failure"}],` +
	`"elements":["Orders API","Payments"],"supersedes":4,"duplicateOf":0}` + "\n```"

func runDecideCmd(
	t *testing.T,
	fake *decideAPI,
	changes []repo.Change,
	args ...string,
) (string, string, error) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ARCHYL_CONFIG_DIR", t.TempDir())
	t.Setenv("ARCHYL_CACHE_DIR", t.TempDir())
	t.Setenv("ARCHYL_NO_CACHE", "1")
	t.Setenv("ARCHYL_NO_KEYRING", "1")
	t.Setenv("ARCHYL_HOST", fake.serve(t))
	t.Setenv("ARCHYL_API_KEY", "test-key")
	t.Chdir(t.TempDir())

	var out, errOut bytes.Buffer
	app := &App{Printer: &ui.Printer{Out: &out, Err: &errOut}}
	deps := decideDeps{
		openRepo: func(context.Context) (decideRepo, error) { return &fakeDecideRepo{changes: changes}, nil },
		editor: func(context.Context, string) error {
			t.Fatal("decide must not open an editor off-terminal")
			return nil
		},
	}
	root := NewRoot(app)
	if existing, _, err := root.Find([]string{"decide"}); err == nil && existing != root {
		root.RemoveCommand(existing)
	}
	root.AddCommand(decideCommand(app, deps))
	root.SetArgs(append(args, "--project", "p1"))
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	err := root.Execute()
	return out.String(), errOut.String(), err
}

var outboxChanges = []repo.Change{
	{Path: "internal/orders/outbox.go", Status: "added"},
	{Path: "internal/orders/outbox_test.go", Status: "added"},
}

func TestDecideCommand(
	t *testing.T,
) {
	testCases := []struct {
		name         string
		reply        string
		failLinksTo  string
		extraTouched int
		changes      []repo.Change
		args         []string
		wantErr      string
		wantExit     int
		wantOut      []string
		wantStderr   []string
		wantMissing  []string
		// wantADR is the decision sent to the API; nil means none was created.
		wantADR   map[string]any
		wantLinks []string
		wantChat  []string
		noChat    bool
	}{
		{
			name:    "piped, the AI draft is printed and nothing is created",
			reply:   decideReply,
			changes: outboxChanges,
			args:    []string{"decide", "use an outbox for order events"},
			wantOut: []string{
				"title: Publish order events through an outbox table",
				"status: proposed",
				"supersedes: ADR-4",
				"elements:\n    - Orders API\n---",
				"## Consequences\n\n**Positive**\n\n- No event is lost when Kafka is down",
				"## Alternatives considered\n\n- Keep publishing in the request: events are lost on failure",
			},
			wantMissing: []string{"Payments", "Recorded"},
			wantChat: []string{
				"Use only the facts below. Do not invent",
				"What the developer says was decided: use an outbox for order events",
				"Branch: feat/outbox (compared with origin/main)",
				"- Orders API (container, in Shop), used by Storefront",
				"- ADR-4 Publish order events straight to Kafka (accepted)",
				"+func WriteOutbox() {}",
			},
		},
		{
			name:    "--yes records it as proposed and links its elements",
			reply:   decideReply,
			changes: outboxChanges,
			args:    []string{"decide", "--yes"},
			wantOut: []string{
				"✓ Recorded ADR-12 Publish order events through an outbox table · proposed",
				"linked to ▣ Orders API",
				"/project/p1/decisions?expandId=adr-12",
				"archyl show ADR-12",
			},
			wantADR: map[string]any{
				"title":        "Publish order events through an outbox table",
				"status":       "proposed",
				"context":      "Order events were sent to Kafka inside the request.\n\nSupersedes ADR-4.",
				"decision":     "Write each event to an outbox table in the order's transaction.\n\n### Alternatives considered\n\n- Keep publishing in the request: events are lost on failure",
				"consequences": "**Positive**\n\n- No event is lost when Kafka is down\n\n**Negative**\n\n- The outbox needs purging",
			},
			wantLinks: []string{`{"elementId":"api","elementType":2}`},
			wantChat:  []string{"The developer did not say what was decided: infer it from the diff."},
		},
		{
			name:        "a link that fails is reported, the decision kept, and the script told",
			reply:       decideReply,
			failLinksTo: "db",
			changes:     outboxChanges,
			args:        []string{"decide", "--no-ai", "--yes", "Use an outbox"},
			wantExit:    1,
			wantOut: []string{
				"✓ Recorded ADR-12 Use an outbox · proposed",
				"linked to ▣ Orders API",
				"✗ not linked to ▣ Orders DB: Failed to create link (HTTP 500)",
				"Add the missing links on the decision in the web app:",
			},
			wantADR: map[string]any{
				"title":    "Use an outbox",
				"status":   "proposed",
				"context":  "The change touches Orders API (container), Orders DB (container). Files changed: internal/orders/outbox.go, internal/orders/outbox_test.go.",
				"decision": "Use an outbox",
			},
			wantLinks: []string{`{"elementId":"api","elementType":2}`, `{"elementId":"db","elementType":2}`},
			noChat:    true,
		},
		{
			name:       "an unusable AI reply falls back to the template",
			reply:      "I would rather not.",
			changes:    outboxChanges,
			args:       []string{"decide", "keep the outbox in Postgres"},
			wantOut:    []string{"title: Keep the outbox in Postgres", "## Decision\n\nkeep the outbox in Postgres"},
			wantStderr: []string{"No AI draft this time (the reply holds no decision draft); starting from the template."},
		},
		{
			name:         "many touched and none named: no link, and a notice",
			reply:        `{"title":"T","decision":"D","elements":["Payments"]}`,
			extraTouched: 4,
			changes:      outboxChanges,
			args:         []string{"decide", "--yes"},
			wantOut:      []string{"✓ Recorded ADR-12 T · proposed"},
			wantMissing:  []string{"linked to"},
			wantStderr:   []string{"The draft links no element: the change touches 6 elements and none was picked."},
			wantADR:      map[string]any{"title": "T", "status": "proposed", "decision": "D"},
		},
		{
			name: "control characters from the AI and the server never reach the terminal",
			reply: `{"title":"Use an outbox\u001b]0;pwned\u0007","decision":"D\u001b[31m",` +
				`"consequences":"\u009b2J","elements":["Orders API"]}`,
			changes:     outboxChanges,
			args:        []string{"decide", "--yes"},
			wantOut:     []string{"title: Use an outbox]0;pwned", "✓ Recorded ADR-12 Use an outbox]0;pwned · proposed"},
			wantMissing: []string{"\x1b", "\x07", "\u009b"},
			wantADR:     map[string]any{"title": "Use an outbox]0;pwned", "status": "proposed", "decision": "D[31m", "consequences": "2J"},
			wantLinks:   []string{`{"elementId":"api","elementType":2}`},
		},
		{
			name:    "nothing changed and nothing said",
			changes: nil,
			args:    []string{"decide", "--yes"},
			wantErr: `nothing to draft from; say what was decided: archyl decide "<what was decided>"`,
			noChat:  true,
		},
		{
			name:    "--no-ai needs to be told what was decided",
			changes: outboxChanges,
			args:    []string{"decide", "--no-ai"},
			wantErr: "nothing to draft from",
			noChat:  true,
		},
		{
			name:    "an unknown base says how to pick another",
			changes: outboxChanges,
			args:    []string{"decide", "x", "--base", "origin/nope"},
			wantErr: "cannot find origin/nope in this clone; fetch it, or compare with another branch: archyl decide --base <branch>",
			noChat:  true,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			fake := &decideAPI{reply: testCase.reply, failLinksTo: testCase.failLinksTo, extraTouched: testCase.extraTouched}
			out, stderr, err := runDecideCmd(t, fake, testCase.changes, testCase.args...)
			var exit *ExitError
			switch {
			case testCase.wantErr != "":
				require.Error(t, err)
				assert.Contains(t, err.Error(), testCase.wantErr)
			case testCase.wantExit != 0:
				require.ErrorAs(t, err, &exit)
				assert.Equal(t, testCase.wantExit, exit.Code)
			default:
				require.NoError(t, err)
			}
			for _, w := range testCase.wantOut {
				assert.Contains(t, out, w)
			}
			for _, w := range testCase.wantMissing {
				assert.NotContains(t, out, w)
			}
			for _, w := range testCase.wantStderr {
				assert.Contains(t, stderr, w)
			}

			created := fake.sent("POST /projects/p1/adrs")
			if testCase.wantADR == nil {
				assert.Empty(t, created, "no decision is created")
			} else {
				require.Len(t, created, 1)
				var got map[string]any
				require.NoError(t, json.Unmarshal([]byte(created[0]), &got))
				assert.Equal(t, testCase.wantADR, got)
			}
			links := fake.sent("POST /adrs/adr-12/links")
			if testCase.wantLinks == nil {
				assert.Empty(t, links)
			} else {
				assert.ElementsMatch(t, testCase.wantLinks, links, "links are made concurrently")
			}

			chats := fake.sent("POST /chat")
			if testCase.noChat {
				assert.Empty(t, chats)
				return
			}
			if len(testCase.wantChat) > 0 {
				require.Len(t, chats, 1)
				var body struct {
					Messages []struct {
						Content string `json:"content"`
					} `json:"messages"`
					ProjectID string `json:"currentProjectId"`
				}
				require.NoError(t, json.Unmarshal([]byte(chats[0]), &body))
				assert.Equal(t, "p1", body.ProjectID)
				for _, w := range testCase.wantChat {
					assert.Contains(t, body.Messages[0].Content, w)
				}
			}
		})
	}
}

func TestDecideCommandJSON(
	t *testing.T,
) {
	testCases := []struct {
		name         string
		args         []string
		failLinksTo  string
		wantCreated  bool
		wantFailures int
	}{
		{name: "the draft only", args: []string{"decide", "--json", "Use an outbox", "--no-ai"}},
		{name: "recorded with --yes", args: []string{"decide", "--json", "Use an outbox", "--no-ai", "--yes"}, wantCreated: true},
		{
			name:         "a failed link is in the JSON and exits 1",
			args:         []string{"decide", "--json", "Use an outbox", "--no-ai", "--yes"},
			failLinksTo:  "api",
			wantCreated:  true,
			wantFailures: 1,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			out, _, err := runDecideCmd(t, &decideAPI{failLinksTo: testCase.failLinksTo}, outboxChanges, testCase.args...)
			if testCase.wantFailures > 0 {
				var exit *ExitError
				require.ErrorAs(t, err, &exit)
				assert.Equal(t, 1, exit.Code)
			} else {
				require.NoError(t, err)
			}
			var got decideResult
			require.NoError(t, json.Unmarshal([]byte(out), &got), out)
			require.NotNil(t, got.Draft)
			assert.Equal(t, "Use an outbox", got.Draft.Title)
			assert.Equal(t, decide.StatusProposed, got.Draft.Status)
			if !testCase.wantCreated {
				assert.Nil(t, got.ADR)
				return
			}
			require.NotNil(t, got.ADR)
			assert.Equal(t, 12, got.ADR.Number)
			assert.Len(t, got.Linked, 2-testCase.wantFailures)
			assert.Len(t, got.LinkFailures, testCase.wantFailures)
			assert.Contains(t, got.URL, "/project/p1/decisions?expandId=adr-12")
		})
	}
}

func TestDecideReviewDraft(
	t *testing.T,
) {
	facts := decide.Facts{
		Touched: []decide.Touched{
			{Element: decide.Element{ID: "api", Name: "Orders API", Type: "container", Path: "Shop / Orders API"}},
		},
	}
	initial := &decide.Draft{Title: "Use an outbox", Status: decide.StatusProposed, Decision: "Outbox.", Elements: facts.Elements()}
	errQuit := errors.New("exit status 1")

	// An edit step writes the file, or fails like a crashed editor.
	type step struct {
		write string
		fail  bool
		// wantOpened is part of the file the editor must be opened on.
		wantOpened string
	}
	testCases := []struct {
		name       string
		choices    []string
		edits      []step
		want       *decide.Draft
		wantOut    string
		wantStderr []string
	}{
		{
			name:    "create as drafted",
			choices: []string{choiceCreate},
			want:    initial,
		},
		{
			name:    "cancel",
			choices: []string{choiceCancel},
			wantOut: "Cancelled; nothing recorded.",
		},
		{
			name:    "emptying the file cancels",
			choices: []string{choiceEdit},
			edits:   []step{{write: "  \n", wantOpened: "title: Use an outbox"}},
			wantOut: "Cancelled: the file was empty; nothing recorded.",
		},
		{
			name:    "a failed editor, then a broken file kept for the next edit, then a fix",
			choices: []string{choiceEdit, choiceEdit, choiceEdit, choiceCreate},
			edits: []step{
				{fail: true, wantOpened: "Delete everything to cancel."},
				{write: "---\ntitle: Use an outbox\nstatus: maybe\n---\n", wantOpened: "title: Use an outbox"},
				{
					write:      "---\ntitle: Use a transactional outbox\nstatus: accepted\nelements: [Orders API, Payments]\n---\n## Decision\n\nOutbox table.\n",
					wantOpened: "status: maybe",
				},
			},
			want: &decide.Draft{
				Title:    "Use a transactional outbox",
				Status:   "accepted",
				Decision: "Outbox table.",
				Elements: facts.Elements(),
			},
			wantStderr: []string{
				"The editor exited with an error: exit status 1; the draft is unchanged.",
				`Status "maybe" is not one of proposed, accepted. Edit it again to fix it; your text is kept.`,
				`"Payments" is not one of the touched elements, so it will not be linked.`,
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("TMPDIR", t.TempDir())
			var out, errOut bytes.Buffer
			p := &ui.Printer{Out: &out, Err: &errOut}

			choices := testCase.choices
			choose := func(*decide.Draft) (string, error) {
				require.NotEmpty(t, choices, "asked more often than expected")
				choice := choices[0]
				choices = choices[1:]
				return choice, nil
			}
			edits := testCase.edits
			editor := func(_ context.Context, path string) error {
				require.NotEmpty(t, edits, "edited more often than expected")
				s := edits[0]
				edits = edits[1:]
				raw, err := os.ReadFile(path)
				require.NoError(t, err)
				assert.Contains(t, string(raw), s.wantOpened)
				if s.fail {
					return errQuit
				}
				return os.WriteFile(path, []byte(s.write), 0o600)
			}

			got, err := reviewDraft(context.Background(), p, editor, choose, facts, initial)
			require.NoError(t, err)
			assert.Equal(t, testCase.want, got)
			assert.Empty(t, choices, "every choice was asked")
			assert.Empty(t, edits, "every edit happened")
			assert.Contains(t, out.String(), "Use an outbox", "the draft is shown")
			assert.Contains(t, out.String(), testCase.wantOut)
			for _, w := range testCase.wantStderr {
				assert.Contains(t, errOut.String(), w)
			}
		})
	}
}

func TestDecideLinkDecision(
	t *testing.T,
) {
	var mu sync.Mutex
	inFlight, peak := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		inFlight++
		peak = max(peak, inFlight)
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		mu.Lock()
		inFlight--
		mu.Unlock()
		raw, _ := io.ReadAll(r.Body)
		if strings.Contains(string(raw), `"e3"`) {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	var elements []decide.Element
	for i := range 10 {
		elements = append(elements, decide.Element{ID: fmt.Sprintf("e%d", i), Name: fmt.Sprintf("E%d", i), Type: "component"})
	}
	linked, failures := linkDecision(context.Background(), api.NewClient(srv.URL, "", nil), "adr-1", elements)

	assert.LessOrEqual(t, peak, decideLinkConcurrency, "a few links at a time")
	assert.Greater(t, peak, 1, "links are made concurrently")
	require.Len(t, failures, 1)
	assert.Equal(t, "e3", failures[0].Element.ID)
	require.Len(t, linked, 9)
	assert.Equal(t, "e0", linked[0].ID, "the elements' order is kept")
	assert.Equal(t, "e9", linked[8].ID)
}
