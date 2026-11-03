package history

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/archyl-com/cli/internal/api"
)

func TestParsePoint(
	t *testing.T,
) {
	testCases := []struct {
		raw     string
		want    Point
		wantErr string
	}{
		{raw: "now", want: Point{Raw: "now", Kind: PointNow}},
		{raw: "NOW", want: Point{Raw: "NOW", Kind: PointNow}},
		{raw: "12", want: Point{Raw: "12", Kind: PointVersion, Number: 12}},
		{raw: "rev12", want: Point{Raw: "rev12", Kind: PointVersion, Number: 12}},
		{raw: "REV 7", want: Point{Raw: "REV 7", Kind: PointVersion, Number: 7}},
		{raw: "r3", want: Point{Raw: "r3", Kind: PointVersion, Number: 3}},
		{raw: " 2026-09-01 ", want: Point{Raw: "2026-09-01", Kind: PointDate, Day: time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local)}},
		{raw: "30d", want: Point{Raw: "30d", Kind: PointAgo, Ago: 30 * 24 * time.Hour}},
		{raw: "2w", want: Point{Raw: "2w", Kind: PointAgo, Ago: 14 * 24 * time.Hour}},
		{raw: "12h", want: Point{Raw: "12h", Kind: PointAgo, Ago: 12 * time.Hour}},
		{raw: "3mo", want: Point{Raw: "3mo", Kind: PointAgo, Ago: 90 * 24 * time.Hour}},
		{raw: "1y", want: Point{Raw: "1y", Kind: PointAgo, Ago: 365 * 24 * time.Hour}},
		{raw: "v1.2.0", want: Point{Raw: "v1.2.0", Kind: PointRelease, Release: "v1.2.0"}},
		{raw: "2026.10", want: Point{Raw: "2026.10", Kind: PointRelease, Release: "2026.10"}},
		{raw: "0", wantErr: "versions start at 1"},
		{raw: "2026-13-01", wantErr: "is not a date"},
		{raw: "  ", wantErr: "empty point"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.raw, func(t *testing.T) {
			got, err := ParsePoint(testCase.raw)
			if testCase.wantErr != "" {
				require.ErrorContains(t, err, testCase.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestParseRange(
	t *testing.T,
) {
	testCases := []struct {
		arg      string
		wantFrom Point
		wantTo   Point
		wantErr  string
	}{
		{
			arg:      "v1.2..v2.0",
			wantFrom: Point{Raw: "v1.2", Kind: PointRelease, Release: "v1.2"},
			wantTo:   Point{Raw: "v2.0", Kind: PointRelease, Release: "v2.0"},
		},
		{
			arg:      "v1.2.0...v2.0.0",
			wantFrom: Point{Raw: "v1.2.0", Kind: PointRelease, Release: "v1.2.0"},
			wantTo:   Point{Raw: "v2.0.0", Kind: PointRelease, Release: "v2.0.0"},
		},
		{
			arg:      "12..",
			wantFrom: Point{Raw: "12", Kind: PointVersion, Number: 12},
			wantTo:   Point{Raw: "now", Kind: PointNow},
		},
		{
			arg:      "..30d",
			wantFrom: Point{Kind: PointOldest},
			wantTo:   Point{Raw: "30d", Kind: PointAgo, Ago: 30 * 24 * time.Hour},
		},
		{
			arg:      "..",
			wantFrom: Point{Kind: PointOldest},
			wantTo:   Point{Raw: "now", Kind: PointNow},
		},
		{arg: "v1..2026-02-30", wantErr: "is not a date"},
		{arg: "v1", wantErr: "is not a range"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.arg, func(t *testing.T) {
			from, to, err := ParseRange(testCase.arg)
			if testCase.wantErr != "" {
				require.ErrorContains(t, err, testCase.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, testCase.wantFrom, from)
			assert.Equal(t, testCase.wantTo, to)
		})
	}
}

func TestLooksLikePoint(
	t *testing.T,
) {
	testCases := []struct {
		arg  string
		want bool
	}{
		{arg: "now", want: true},
		{arg: "rev4", want: true},
		{arg: "2026-09-01", want: true},
		{arg: "2w", want: true},
		{arg: "v1.2.0", want: false},
		{arg: "Billing API", want: false},
	}
	for _, testCase := range testCases {
		t.Run(testCase.arg, func(t *testing.T) {
			assert.Equal(t, testCase.want, LooksLikePoint(testCase.arg))
			assert.False(t, IsRange(testCase.arg))
		})
	}
}

// versions are REV 10 to 13, a week apart from 2026-09-01.
func versions() []api.Version {
	start := time.Date(2026, 9, 1, 12, 0, 0, 0, time.Local)
	var out []api.Version
	for i := range 4 {
		out = append(out, api.Version{Number: 10 + i, CreatedAt: start.AddDate(0, 0, 7*i)})
	}
	return out
}

func TestResolve(
	t *testing.T,
) {
	at := func(day, hour int) *time.Time {
		v := time.Date(2026, 9, day, hour, 0, 0, 0, time.Local)
		return &v
	}
	src := Sources{
		Versions: versions(),
		Releases: []api.ReleaseDetail{
			{Version: "v1.2.0", ReleasedAt: at(9, 9), CreatedAt: *at(9, 9)},
			{Version: "v1.2.0", ReleasedAt: at(10, 9), CreatedAt: *at(10, 9)},
			{Version: "v1.3.0", CreatedAt: *at(15, 18)},
			{Version: "v0.9.0", CreatedAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.Local)},
		},
		Now: time.Date(2026, 9, 30, 12, 0, 0, 0, time.Local),
	}
	testCases := []struct {
		name       string
		raw        string
		oldest     bool
		wantNumber int
		wantLabel  string
		wantString string
		wantErr    string
	}{
		{name: "now", raw: "now", wantLabel: "now", wantString: "now"},
		{name: "the oldest", oldest: true, wantNumber: 10, wantLabel: "REV 10", wantString: "REV 10 (2026-09-01)"},
		{name: "a number", raw: "rev12", wantNumber: 12, wantLabel: "REV 12", wantString: "REV 12 (2026-09-15)"},
		{name: "a number not kept", raw: "3", wantErr: "there is no REV 3: Archyl keeps REV 10 to REV 13"},
		{name: "a date takes its whole day", raw: "2026-09-15", wantNumber: 12, wantLabel: "2026-09-15", wantString: "2026-09-15 (REV 12, 2026-09-15)"},
		{name: "a date between versions", raw: "2026-09-20", wantNumber: 12},
		{name: "a date before every version", raw: "2026-08-31", wantErr: "no version at or before 2026-08-31: the oldest Archyl keeps is REV 10 from 2026-09-01"},
		{name: "a duration", raw: "2w", wantNumber: 12, wantLabel: "2w ago"},
		{name: "a release, first time it shipped", raw: "v1.2.0", wantNumber: 11, wantLabel: "v1.2.0", wantString: "v1.2.0 (REV 11, 2026-09-08)"},
		{name: "a release without its v", raw: "1.3.0", wantNumber: 12, wantLabel: "v1.3.0"},
		{name: "a release before every version", raw: "v0.9.0", wantErr: "no version at or before release v0.9.0 (2026-08-01)"},
		{name: "an unknown release", raw: "v1.4.0", wantErr: `no release "v1.4.0"; the closest are v1.2.0, v1.3.0, v0.9.0`},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			p := Point{Kind: PointOldest}
			if !testCase.oldest {
				var err error
				p, err = ParsePoint(testCase.raw)
				require.NoError(t, err)
			}

			got, err := Resolve(p, src)

			if testCase.wantErr != "" {
				require.ErrorContains(t, err, testCase.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, testCase.wantNumber, got.Number())
			assert.Equal(t, testCase.wantNumber == 0, got.IsNow())
			if testCase.wantLabel != "" {
				assert.Equal(t, testCase.wantLabel, got.Label)
			}
			if testCase.wantString != "" {
				assert.Equal(t, testCase.wantString, got.String())
			}
		})
	}
}

func TestResolveWithout(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		raw     string
		src     Sources
		wantErr string
		wantIs  error
	}{
		{name: "no versions", raw: "12", wantIs: ErrNoVersions},
		{name: "now needs none", raw: "now"},
		{name: "no releases", raw: "v1.0.0", src: Sources{Versions: versions()}, wantErr: "none is recorded for this project; record one with archyl release <version>"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			p, err := ParsePoint(testCase.raw)
			require.NoError(t, err)

			_, err = Resolve(p, testCase.src)

			switch {
			case testCase.wantIs != nil:
				require.ErrorIs(t, err, testCase.wantIs)
			case testCase.wantErr != "":
				require.ErrorContains(t, err, testCase.wantErr)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestDefaultRange(
	t *testing.T,
) {
	testCases := []struct {
		name     string
		versions []api.Version
		wantFrom Point
	}{
		{name: "the version before the latest", versions: versions(), wantFrom: Point{Raw: "12", Kind: PointVersion, Number: 12}},
		{name: "a single version", versions: versions()[:1], wantFrom: Point{Kind: PointOldest}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			from, to := DefaultRange(testCase.versions)
			assert.Equal(t, testCase.wantFrom, from)
			assert.Equal(t, PointNow, to.Kind)
		})
	}
}

func TestLevenshtein(
	t *testing.T,
) {
	testCases := []struct {
		a, b string
		want int
	}{
		{a: "", b: "abc", want: 3},
		{a: "1.2.0", b: "1.2.0", want: 0},
		{a: "1.4.0", b: "1.3.0", want: 1},
		{a: "kitten", b: "sitting", want: 3},
	}
	for _, testCase := range testCases {
		t.Run(testCase.a+"→"+testCase.b, func(t *testing.T) {
			assert.Equal(t, testCase.want, levenshtein(testCase.a, testCase.b))
		})
	}
}
