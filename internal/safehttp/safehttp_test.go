package safehttp

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSameOrigin(t *testing.T) {
	testCases := []struct {
		name   string
		to     string
		wantOK bool
	}{
		{name: "same origin", to: "https://api.example.com/v2", wantOK: true},
		{name: "host case", to: "https://API.example.com/v2", wantOK: true},
		{name: "other host", to: "https://evil.example/v2"},
		{name: "other port", to: "https://api.example.com:8443/v2"},
		{name: "downgrade", to: "http://api.example.com/v2"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			from, _ := url.Parse("https://api.example.com/v1")
			to, _ := url.Parse(testCase.to)
			err := SameOrigin(&http.Request{URL: to}, []*http.Request{{URL: from}})
			assert.Equal(t, testCase.wantOK, err == nil, "%v", err)
		})
	}
}
