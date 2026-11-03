package repo

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBlame(
	t *testing.T,
) {
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	write(t, filepath.Join(dir, "a.go"), "one\ntwo\n")
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-q", "-m", "first version")
	write(t, filepath.Join(dir, "a.go"), "one\nTWO\n")
	gitIn(t, dir, "commit", "-q", "-am", "shout the second line")
	write(t, filepath.Join(dir, "a.go"), "one\nTWO\nthree\n")

	g, err := Open(context.Background(), dir)
	require.NoError(t, err)

	testCases := []struct {
		name            string
		line            int
		wantSubject     string
		wantUncommitted bool
	}{
		{name: "line from the first commit", line: 1, wantSubject: "first version"},
		{name: "line changed later", line: 2, wantSubject: "shout the second line"},
		{name: "line not committed yet", line: 3, wantUncommitted: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			c, err := g.Blame(context.Background(), "a.go", testCase.line)
			require.NoError(t, err)
			assert.Equal(t, testCase.wantUncommitted, c.Uncommitted)
			if !testCase.wantUncommitted {
				assert.Equal(t, testCase.wantSubject, c.Subject)
				assert.Equal(t, "t", c.Author)
				assert.Equal(t, "t@t", c.Email)
				assert.False(t, c.Date.IsZero())
				assert.Len(t, c.Short(), 8)
			}
		})
	}

	_, err = g.Blame(context.Background(), "a.go", 99)
	assert.Error(t, err, "a line past the end of the file")

	last, err := g.LastCommit(context.Background(), "a.go")
	require.NoError(t, err)
	assert.Equal(t, "shout the second line", last.Subject)

	none, err := g.LastCommit(context.Background(), "untracked.go")
	require.NoError(t, err)
	assert.Nil(t, none)
}
