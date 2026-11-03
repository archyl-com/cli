package lsp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
)

// maxMessage bounds one message body; editors send whole files on save.
const maxMessage = 64 << 20

// JSON-RPC error codes the server answers with.
const (
	codeParseError     = -32700
	codeInvalidParams  = -32602
	codeMethodNotFound = -32601
	codeInternalError  = -32603
	codeNotInitialized = -32002
)

// message is any JSON-RPC message read from the client: a request (ID and
// method), a notification (method only) or a response to one of ours.
type message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
}

func (m *message) isRequest() bool { return len(m.ID) > 0 && m.Method != "" }

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// reader reads Content-Length framed messages.
type reader struct {
	r *bufio.Reader
}

func newReader(r io.Reader) *reader {
	return &reader{r: bufio.NewReaderSize(r, 64<<10)}
}

func (r *reader) read() ([]byte, error) {
	headers, err := textproto.NewReader(r.r).ReadMIMEHeader()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, io.EOF
		}
		return nil, fmt.Errorf("read headers: %w", err)
	}
	length, err := strconv.Atoi(strings.TrimSpace(headers.Get("Content-Length")))
	if err != nil || length < 0 {
		return nil, fmt.Errorf("bad Content-Length %q", headers.Get("Content-Length"))
	}
	if length > maxMessage {
		return nil, fmt.Errorf("message of %d bytes is over the %d limit", length, maxMessage)
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r.r, body); err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	return body, nil
}

// writer writes framed messages; it is safe for concurrent use and drops
// everything once closed, so late timers cannot write after exit.
type writer struct {
	mu     sync.Mutex
	w      io.Writer
	closed bool
}

func (w *writer) write(v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	if _, err := fmt.Fprintf(w.w, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	_, err = w.w.Write(body)
	return err
}

func (w *writer) close() {
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result"`
}

type errorResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Error   rpcError        `json:"error"`
}

type notification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type request struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}
