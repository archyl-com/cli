package update

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewer(
	t *testing.T,
) {
	testCases := []struct {
		current, latest string
		want            bool
	}{
		{current: "0.2.0", latest: "v0.3.0", want: true},
		{current: "v1.2.3", latest: "v1.2.10", want: true},
		{current: "1.2.3", latest: "1.2.3", want: false},
		{current: "1.3.0", latest: "1.2.9", want: false},
		{current: "1.2.3-rc.1", latest: "1.2.3", want: false},
		{current: "dev", latest: "1.0.0", want: false},
		{current: "1.0.0", latest: "", want: false},
	}
	for _, testCase := range testCases {
		t.Run(testCase.current+"→"+testCase.latest, func(t *testing.T) {
			assert.Equal(t, testCase.want, Newer(testCase.current, testCase.latest))
		})
	}
}
