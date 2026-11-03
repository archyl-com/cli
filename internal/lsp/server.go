// Package lsp is a small Language Server that brings Archyl into any editor:
// conformance violations as diagnostics on save, the file's element on
// hover, and code actions to ask why or open the element in Archyl. It speaks
// only the slice of the protocol it needs, over stdio, and never takes the
// editor down: failures become one warning in the editor's log.
package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf16"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/report"
)

// Commands the server executes.
const (
	CommandWhy  = "archyl.why"
	CommandOpen = "archyl.open"
)

const (
	defaultDebounce = 300 * time.Millisecond
	hoverTTL        = 10 * time.Minute
	requestTimeout  = 30 * time.Second
	askTimeout      = 3 * time.Minute
)

// Opener connects the server to a workspace: it returns the backend and the
// repository root that paths are relative to.
type Opener func(ctx context.Context, root string) (Backend, string, error)

// Options configures a Server.
type Options struct {
	Open    Opener
	Version string
	// Debounce delays a check after a save, so a burst of saves runs one.
	Debounce time.Duration
	// TempDir receives the answers to "Ask Archyl why"; os.TempDir by default.
	TempDir string
}

// Server is one language server connection.
type Server struct {
	o Options
	w *writer

	backend      Backend
	root         string
	openErr      error
	showDocument bool

	mu       sync.Mutex
	timers   map[string]*time.Timer
	texts    map[string]string
	hovers   map[string]hoverEntry
	ready    bool
	degraded bool
	nextID   atomic.Int64
	ctx      context.Context
}

type hoverEntry struct {
	brief *Brief
	at    time.Time
}

// NewServer returns a server; Serve runs it.
func NewServer(o Options) *Server {
	if o.Debounce <= 0 {
		o.Debounce = defaultDebounce
	}
	if o.TempDir == "" {
		o.TempDir = os.TempDir()
	}
	return &Server{o: o, timers: map[string]*time.Timer{}, texts: map[string]string{}, hovers: map[string]hoverEntry{}}
}

// Serve reads messages from in and answers on out until the client sends
// exit or closes the stream.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	s.ctx = ctx
	s.w = &writer{w: out}
	defer s.stop(cancel)

	r := newReader(in)
	for {
		body, err := r.read()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		var msg message
		if err := json.Unmarshal(body, &msg); err != nil {
			s.replyError(nil, codeParseError, "invalid JSON")
			continue
		}
		if msg.Method == "" {
			continue // a response to one of our requests
		}
		if msg.Method == "exit" {
			return nil
		}
		s.dispatch(ctx, &msg)
	}
}

// stop cancels pending checks and in-flight requests; whatever finishes
// late is dropped by the closed writer.
func (s *Server) stop(cancel context.CancelFunc) {
	s.mu.Lock()
	for _, t := range s.timers {
		t.Stop()
	}
	s.mu.Unlock()
	cancel()
	s.w.close()
}

func (s *Server) dispatch(ctx context.Context, msg *message) {
	switch msg.Method {
	case "initialize":
		// Answered inline: nothing else may run before it.
		s.safely(func() { s.initialize(ctx, msg) })
		return
	case "initialized":
		if s.openErr != nil {
			s.warn(s.openErr)
		}
		return
	case "shutdown":
		s.reply(msg.ID, nil)
		return
	case "textDocument/didOpen":
		var p didOpenParams
		if json.Unmarshal(msg.Params, &p) == nil {
			s.remember(p.TextDocument.URI, p.TextDocument.Text)
			s.schedule(p.TextDocument.URI)
		}
		return
	case "textDocument/didSave":
		var p didSaveParams
		if json.Unmarshal(msg.Params, &p) == nil {
			if p.Text != nil {
				s.remember(p.TextDocument.URI, *p.Text)
			} else {
				s.forget(p.TextDocument.URI)
			}
			s.invalidateHover(p.TextDocument.URI)
			s.schedule(p.TextDocument.URI)
		}
		return
	case "textDocument/didClose":
		var p didCloseParams
		if json.Unmarshal(msg.Params, &p) == nil {
			s.close(p.TextDocument.URI)
		}
		return
	}
	if !msg.isRequest() {
		return // notifications the server does not handle
	}
	if !s.initialized() {
		s.replyError(msg.ID, codeNotInitialized, "initialize first")
		return
	}

	handlers := map[string]func(context.Context, json.RawMessage) (any, error){
		"textDocument/hover":       s.hover,
		"textDocument/codeAction":  s.codeAction,
		"workspace/executeCommand": s.executeCommand,
	}
	handle, ok := handlers[msg.Method]
	if !ok {
		s.replyError(msg.ID, codeMethodNotFound, "method not supported: "+msg.Method)
		return
	}
	go func() {
		var result any
		var err error
		s.safely(func() { result, err = handle(ctx, msg.Params) })
		if err != nil {
			s.replyError(msg.ID, codeInternalError, err.Error())
			return
		}
		s.reply(msg.ID, result)
	}()
}

func (s *Server) initialized() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ready
}

func (s *Server) initialize(ctx context.Context, msg *message) {
	var p initializeParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		s.replyError(msg.ID, codeInvalidParams, "invalid initialize params")
		return
	}
	root := firstNonEmpty(uriToPath(p.RootURI), p.RootPath)
	if root == "" && len(p.WorkspaceFolders) > 0 {
		root = uriToPath(p.WorkspaceFolders[0].URI)
	}
	if root == "" {
		root, _ = os.Getwd()
	}

	var backend Backend
	var repoRoot string
	err := errors.New("archyl lsp has no workspace opener")
	if s.o.Open != nil {
		backend, repoRoot, err = s.o.Open(ctx, root)
	}
	s.mu.Lock()
	s.ready = true
	s.showDocument = p.Capabilities.Window.ShowDocument.Support
	if err != nil {
		s.openErr = err
	} else {
		s.backend, s.root = backend, repoRoot
	}
	s.mu.Unlock()

	s.reply(msg.ID, map[string]any{
		"capabilities": map[string]any{
			"textDocumentSync": map[string]any{
				"openClose": true,
				"change":    0,
				"save":      map[string]any{"includeText": true},
			},
			"hoverProvider":      true,
			"codeActionProvider": true,
			"executeCommandProvider": map[string]any{
				"commands": []string{CommandWhy, CommandOpen},
			},
		},
		"serverInfo": map[string]any{"name": "archyl", "version": s.o.Version},
	})
}

// path resolves a document URI to the backend and the repository path.
func (s *Server) path(uri string) (Backend, string) {
	s.mu.Lock()
	backend, root := s.backend, s.root
	s.mu.Unlock()
	if backend == nil {
		return nil, ""
	}
	rel := relPath(root, uriToPath(uri))
	if rel == "" || strings.HasPrefix(rel, ".git/") {
		return nil, ""
	}
	return backend, rel
}

func (s *Server) remember(uri, text string) {
	s.mu.Lock()
	s.texts[uri] = text
	s.mu.Unlock()
}

func (s *Server) forget(uri string) {
	s.mu.Lock()
	delete(s.texts, uri)
	s.mu.Unlock()
}

func (s *Server) close(uri string) {
	s.mu.Lock()
	if t := s.timers[uri]; t != nil {
		t.Stop()
		delete(s.timers, uri)
	}
	delete(s.texts, uri)
	delete(s.hovers, uri)
	s.mu.Unlock()
	s.publish(uri, nil)
}

// schedule checks a document once its saves settle.
func (s *Server) schedule(uri string) {
	if backend, _ := s.path(uri); backend == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if t := s.timers[uri]; t != nil {
		t.Stop()
	}
	s.timers[uri] = time.AfterFunc(s.o.Debounce, func() {
		s.safely(func() { s.diagnose(uri) })
	})
}

func (s *Server) diagnose(uri string) {
	backend, rel := s.path(uri)
	if backend == nil || s.ctx.Err() != nil {
		return
	}
	s.mu.Lock()
	text, ok := s.texts[uri]
	s.mu.Unlock()
	if !ok {
		raw, err := os.ReadFile(uriToPath(uri))
		if err != nil {
			return
		}
		text = string(raw)
	}

	ctx, cancel := context.WithTimeout(s.ctx, requestTimeout)
	defer cancel()
	violations, names, err := backend.Check(ctx, rel, text)
	if err != nil {
		// Offline or refused: show nothing rather than stale findings.
		s.warn(err)
		s.publish(uri, nil)
		return
	}
	s.recovered()
	s.publish(uri, diagnostics(violations, names, text))
}

func (s *Server) publish(uri string, diags []diagnostic) {
	if diags == nil {
		diags = []diagnostic{}
	}
	s.notify("textDocument/publishDiagnostics", publishDiagnosticsParams{URI: uri, Diagnostics: diags})
}

// diagnostics maps violations onto the lines they point at.
func diagnostics(violations []api.Violation, names map[string]string, text string) []diagnostic {
	lines := strings.Split(text, "\n")
	out := make([]diagnostic, 0, len(violations))
	for _, v := range violations {
		start := max(v.LineStart-1, 0)
		end := max(v.LineEnd-1, start)
		if len(lines) > 0 {
			start = min(start, len(lines)-1)
			end = min(end, len(lines)-1)
		}
		endChar := 0
		if end < len(lines) {
			endChar = len(utf16.Encode([]rune(strings.TrimRight(lines[end], "\r"))))
		}
		message := v.Title
		if d := strings.TrimSpace(v.Description); d != "" && d != v.Title {
			message += "\n" + d
		}
		if sug := strings.TrimSpace(v.Suggestion); sug != "" {
			message += "\n→ " + sug
		}
		rule := names[v.RuleID]
		out = append(out, diagnostic{
			Range:    lspRange{Start: position{Line: start}, End: position{Line: end, Character: endChar}},
			Severity: severity(v.Severity),
			Code:     firstNonEmpty(rule, v.RuleID),
			Source:   "archyl",
			Message:  message,
			Data:     &diagnosticRef{RuleID: v.RuleID, Rule: rule, Title: v.Title},
		})
	}
	return out
}

func severity(s string) int {
	switch report.Severity(s) {
	case 4, 3:
		return severityError
	case 2:
		return severityWarning
	case 1:
		return severityInformation
	}
	return severityHint
}

func (s *Server) invalidateHover(uri string) {
	s.mu.Lock()
	delete(s.hovers, uri)
	s.mu.Unlock()
}

// brief returns the file's element, cached per file.
func (s *Server) brief(ctx context.Context, uri string) (*Brief, error) {
	backend, rel := s.path(uri)
	if backend == nil {
		return nil, nil
	}
	s.mu.Lock()
	entry, ok := s.hovers[uri]
	s.mu.Unlock()
	if ok && time.Since(entry.at) < hoverTTL {
		return entry.brief, nil
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	brief, err := backend.Brief(ctx, rel)
	if err != nil {
		s.warn(err)
		return nil, nil
	}
	s.recovered()
	s.mu.Lock()
	s.hovers[uri] = hoverEntry{brief: brief, at: time.Now()}
	s.mu.Unlock()
	return brief, nil
}

func (s *Server) hover(ctx context.Context, raw json.RawMessage) (any, error) {
	var p textDocumentPositionParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	brief, err := s.brief(ctx, p.TextDocument.URI)
	if err != nil || brief == nil {
		return nil, err
	}
	return hover{Contents: markupContent{Kind: "markdown", Value: hoverMarkdown(brief)}}, nil
}

func hoverMarkdown(b *Brief) string {
	var md strings.Builder
	fmt.Fprintf(&md, "**%s** · %s", b.Name, b.Level)
	if b.Path != "" && b.Path != b.Name {
		fmt.Fprintf(&md, "  \n`%s`", b.Path)
	}
	md.WriteString("\n\n")
	if d := strings.TrimSpace(b.Description); d != "" {
		md.WriteString(truncate(d, 400) + "\n\n")
	}
	if len(b.Owners) > 0 {
		fmt.Fprintf(&md, "- **Owners:** %s\n", strings.Join(b.Owners, ", "))
	}
	fmt.Fprintf(&md, "- **Dependents:** %d\n", b.Dependents)
	if len(b.Decisions) > 0 {
		md.WriteString("- **Decisions:**\n")
		for i, adr := range b.Decisions {
			if i == 5 {
				fmt.Fprintf(&md, "  - +%d more\n", len(b.Decisions)-5)
				break
			}
			fmt.Fprintf(&md, "  - ADR-%d %s (%s)\n", adr.Number, adr.Title, adr.Status)
		}
	}
	if b.URL != "" {
		fmt.Fprintf(&md, "\n[Open in Archyl](%s)", b.URL)
	}
	return strings.TrimRight(md.String(), "\n")
}

func (s *Server) codeAction(_ context.Context, raw json.RawMessage) (any, error) {
	var p codeActionParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	actions := []codeAction{}
	if backend, _ := s.path(p.TextDocument.URI); backend == nil {
		return actions, nil
	}
	for _, d := range p.Context.Diagnostics {
		if d.Source != "archyl" {
			continue
		}
		actions = append(actions, codeAction{
			Title:       "Ask Archyl why: " + firstNonEmpty(d.Code, "this rule"),
			Kind:        "quickfix",
			Diagnostics: []diagnostic{d},
			Command: &command{Title: "Ask Archyl why", Command: CommandWhy, Arguments: []any{
				commandArgument{URI: p.TextDocument.URI, Line: d.Range.Start.Line, Ref: d.Data},
			}},
		})
	}
	if len(actions) == 0 {
		actions = append(actions, codeAction{
			Title: "Ask Archyl why",
			Command: &command{Title: "Ask Archyl why", Command: CommandWhy, Arguments: []any{
				commandArgument{URI: p.TextDocument.URI, Line: p.Range.Start.Line},
			}},
		})
	}
	actions = append(actions, codeAction{
		Title:   "Open in Archyl",
		Command: &command{Title: "Open in Archyl", Command: CommandOpen, Arguments: []any{commandArgument{URI: p.TextDocument.URI}}},
	})
	return actions, nil
}

func (s *Server) executeCommand(ctx context.Context, raw json.RawMessage) (any, error) {
	var p executeCommandParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	if len(p.Arguments) == 0 {
		return nil, errors.New("missing argument")
	}
	arg := p.Arguments[0]
	backend, rel := s.path(arg.URI)
	if backend == nil {
		return nil, nil
	}
	switch p.Command {
	case CommandOpen:
		link := backend.ProjectURL()
		if brief, _ := s.brief(ctx, arg.URI); brief != nil && brief.URL != "" {
			link = brief.URL
		}
		if link == "" {
			return nil, nil
		}
		s.show(link, true)
		return nil, nil
	case CommandWhy:
		go s.safely(func() { s.why(backend, rel, arg) })
		return nil, nil
	}
	return nil, fmt.Errorf("unknown command %q", p.Command)
}

// why asks the assistant in the background and shows the answer as a
// markdown document, so a long explanation stays readable.
func (s *Server) why(backend Backend, rel string, arg commandArgument) {
	s.notify("window/showMessage", messageParams{Type: messageInfo, Message: "Asking Archyl…"})
	ctx, cancel := context.WithTimeout(s.ctx, askTimeout)
	defer cancel()

	question := fmt.Sprintf("Why is `%s` built the way it is? Which element does it belong to, what depends on it, and which decisions shape it?", rel)
	if brief, _ := s.brief(ctx, arg.URI); brief != nil {
		question = fmt.Sprintf("Why is `%s` (part of the %s %q) built the way it is? What depends on it, and which decisions and rules shape it?", rel, brief.Level, brief.Name)
	}
	if arg.Ref != nil && arg.Ref.Title != "" {
		question = fmt.Sprintf("Archyl's architecture rule %q flags `%s` line %d: %s. Why does this rule exist, which decisions back it, and how should I change the code to comply?",
			firstNonEmpty(arg.Ref.Rule, arg.Ref.RuleID), rel, arg.Line+1, arg.Ref.Title)
	}
	answer, err := backend.Ask(ctx, question)
	if err != nil {
		s.warn(err)
		s.notify("window/showMessage", messageParams{Type: messageWarning, Message: "Archyl could not answer: " + err.Error()})
		return
	}
	s.recovered()
	if answer == "" {
		answer = "_Archyl had no answer._"
	}
	doc := fmt.Sprintf("# Why: %s\n\n> %s\n\n%s\n", rel, question, answer)

	f, err := os.CreateTemp(s.o.TempDir, "archyl-why-*.md")
	if err == nil {
		_, err = f.WriteString(doc)
		_ = f.Close()
	}
	if err != nil || !s.canShowDocument() {
		s.notify("window/showMessage", messageParams{Type: messageInfo, Message: truncate(answer, 2000)})
		return
	}
	s.show(pathToURI(filepath.Clean(f.Name())), false)
}

func (s *Server) canShowDocument() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.showDocument
}

// show opens a document in the editor, or a link in the browser. Without
// window/showDocument, the link goes to a message the user can copy.
func (s *Server) show(uri string, external bool) {
	if !s.canShowDocument() {
		s.notify("window/showMessage", messageParams{Type: messageInfo, Message: "Open in Archyl: " + uri})
		return
	}
	_ = s.w.write(request{JSONRPC: "2.0", ID: s.nextID.Add(1), Method: "window/showDocument",
		Params: showDocumentParams{URI: uri, External: external, TakeFocus: true}})
}

// warn logs a failure once, until the server works again: an editor gets
// one line when Archyl is unreachable, not one per keystroke.
func (s *Server) warn(err error) {
	s.mu.Lock()
	already := s.degraded
	s.degraded = true
	s.mu.Unlock()
	if !already {
		s.notify("window/logMessage", messageParams{Type: messageWarning, Message: "archyl: " + err.Error()})
	}
}

func (s *Server) recovered() {
	s.mu.Lock()
	s.degraded = false
	s.mu.Unlock()
}

// safely turns a panic into a logged warning: the editor keeps its server.
func (s *Server) safely(fn func()) {
	defer func() {
		if r := recover(); r != nil {
			s.warn(fmt.Errorf("internal error: %v", r))
		}
	}()
	fn()
}

func (s *Server) notify(method string, params any) {
	_ = s.w.write(notification{JSONRPC: "2.0", Method: method, Params: params})
}

func (s *Server) reply(id json.RawMessage, result any) {
	_ = s.w.write(response{JSONRPC: "2.0", ID: id, Result: result})
}

func (s *Server) replyError(id json.RawMessage, code int, msg string) {
	if id == nil {
		id = json.RawMessage("null")
	}
	_ = s.w.write(errorResponse{JSONRPC: "2.0", ID: id, Error: rpcError{Code: code, Message: msg}})
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
