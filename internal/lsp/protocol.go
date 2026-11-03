package lsp

// The slice of the Language Server Protocol the server speaks.

type initializeParams struct {
	RootURI          string            `json:"rootUri"`
	RootPath         string            `json:"rootPath"`
	WorkspaceFolders []workspaceFolder `json:"workspaceFolders"`
	Capabilities     struct {
		Window struct {
			ShowDocument struct {
				Support bool `json:"support"`
			} `json:"showDocument"`
		} `json:"window"`
	} `json:"capabilities"`
}

type workspaceFolder struct {
	URI  string `json:"uri"`
	Name string `json:"name"`
}

type textDocumentItem struct {
	URI  string `json:"uri"`
	Text string `json:"text"`
}

type textDocumentIdentifier struct {
	URI string `json:"uri"`
}

type didOpenParams struct {
	TextDocument textDocumentItem `json:"textDocument"`
}

type didSaveParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Text         *string                `json:"text"`
}

type didCloseParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
}

type position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type lspRange struct {
	Start position `json:"start"`
	End   position `json:"end"`
}

type textDocumentPositionParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Position     position               `json:"position"`
}

// Diagnostic severities.
const (
	severityError       = 1
	severityWarning     = 2
	severityInformation = 3
	severityHint        = 4
)

type diagnostic struct {
	Range    lspRange       `json:"range"`
	Severity int            `json:"severity"`
	Code     string         `json:"code,omitempty"`
	Source   string         `json:"source"`
	Message  string         `json:"message"`
	Data     *diagnosticRef `json:"data,omitempty"`
}

// diagnosticRef travels with a diagnostic so a code action can ask about it.
type diagnosticRef struct {
	RuleID string `json:"ruleId,omitempty"`
	Rule   string `json:"rule,omitempty"`
	Title  string `json:"title,omitempty"`
}

type publishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Diagnostics []diagnostic `json:"diagnostics"`
}

type markupContent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

type hover struct {
	Contents markupContent `json:"contents"`
}

type codeActionParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Range        lspRange               `json:"range"`
	Context      struct {
		Diagnostics []diagnostic `json:"diagnostics"`
	} `json:"context"`
}

type command struct {
	Title     string `json:"title"`
	Command   string `json:"command"`
	Arguments []any  `json:"arguments,omitempty"`
}

type codeAction struct {
	Title       string       `json:"title"`
	Kind        string       `json:"kind,omitempty"`
	Diagnostics []diagnostic `json:"diagnostics,omitempty"`
	Command     *command     `json:"command,omitempty"`
}

type executeCommandParams struct {
	Command   string            `json:"command"`
	Arguments []commandArgument `json:"arguments"`
}

// commandArgument is the single argument of the server's commands.
type commandArgument struct {
	URI  string         `json:"uri"`
	Line int            `json:"line"`
	Ref  *diagnosticRef `json:"ref,omitempty"`
}

type showDocumentParams struct {
	URI       string `json:"uri"`
	External  bool   `json:"external,omitempty"`
	TakeFocus bool   `json:"takeFocus,omitempty"`
}

// Message types for window/logMessage and window/showMessage.
const (
	messageWarning = 2
	messageInfo    = 3
)

type messageParams struct {
	Type    int    `json:"type"`
	Message string `json:"message"`
}
