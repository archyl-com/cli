package cache

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRoundTrip(
	t *testing.T,
) {
	t.Setenv("ARCHYL_CACHE_DIR", t.TempDir())

	miss, err := Load[int]("score")
	require.NoError(t, err)
	assert.Nil(t, miss)
	assert.False(t, miss.Fresh(time.Hour), "a miss is never fresh")

	require.NoError(t, Save("score", 82))
	hit, err := Load[int]("score")
	require.NoError(t, err)
	assert.Equal(t, 82, hit.Value)
	assert.True(t, hit.Fresh(time.Minute))
	assert.False(t, hit.Fresh(0))
}
