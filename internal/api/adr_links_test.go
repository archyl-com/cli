package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLinkADR(
	t *testing.T,
) {
	testCases := []struct {
		name     string
		level    string
		status   int
		wantType float64
		wantErr  string
	}{
		{name: "system", level: "system", status: http.StatusCreated, wantType: 1},
		{name: "container", level: "container", status: http.StatusCreated, wantType: 2},
		{name: "component", level: "component", status: http.StatusCreated, wantType: 3},
		{name: "code", level: "code", status: http.StatusCreated, wantType: 4},
		{name: "unknown level is refused before any call", level: "person", wantErr: `cannot link a decision to a "person" element`},
		{name: "server error", level: "container", status: http.StatusForbidden, wantType: 2, wantErr: "no access (HTTP 403)"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var (
				called bool
				path   string
				body   map[string]any
			)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				path = r.Method + " " + r.URL.Path
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				w.WriteHeader(testCase.status)
				if testCase.status >= 300 {
					_, _ = w.Write([]byte(`{"error":"no access"}`))
					return
				}
				_, _ = w.Write([]byte(`{"id":"l1"}`))
			}))
			defer server.Close()

			err := NewClient(server.URL, "", nil).LinkADR(context.Background(), "adr-1", "el-1", testCase.level)
			if testCase.wantErr != "" {
				require.EqualError(t, err, testCase.wantErr)
			} else {
				require.NoError(t, err)
			}
			if testCase.wantType == 0 {
				assert.False(t, called)
				return
			}
			assert.Equal(t, "POST /api/v1/adrs/adr-1/links", path)
			assert.Equal(t, map[string]any{"elementId": "el-1", "elementType": testCase.wantType}, body)
		})
	}
}
