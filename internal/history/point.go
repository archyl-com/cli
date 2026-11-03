package history

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/archyl-com/cli/internal/api"
)

// PointKind is how a point of history is named.
type PointKind int

// Kinds of point.
const (
	// PointNow is the current model.
	PointNow PointKind = iota
	// PointOldest is the oldest version kept, for a range with no start.
	PointOldest
	// PointVersion is a version number: 12, rev12, r12.
	PointVersion
	// PointDate is the end of a day: 2026-09-01.
	PointDate
	// PointAgo is a time before now: 30d, 2w, 12h, 3mo, 1y.
	PointAgo
	// PointRelease is a release version, as archyl release records it.
	PointRelease
)

// Point is a point of history as written, before it is found among the
// versions.
type Point struct {
	Raw     string        `json:"raw"`
	Kind    PointKind     `json:"-"`
	Number  int           `json:"-"`
	Day     time.Time     `json:"-"`
	Ago     time.Duration `json:"-"`
	Release string        `json:"-"`
}

var (
	revPattern  = regexp.MustCompile(`^(?i)(?:rev|r)?\s*(\d+)$`)
	datePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	agoPattern  = regexp.MustCompile(`^(?i)(\d+)\s*(h|d|w|mo|y)$`)
)

// ParsePoint reads one point. Anything that is not now, a version number,
// a date or a duration is taken for a release version.
func ParsePoint(raw string) (Point, error) {
	s := strings.TrimSpace(raw)
	p := Point{Raw: s}
	switch {
	case s == "":
		return p, errors.New("empty point: name a version (12), a release (v1.2.0), a date (2026-09-01), a duration (30d) or now")
	case strings.EqualFold(s, "now"), strings.EqualFold(s, "head"):
		p.Kind = PointNow
	case revPattern.MatchString(s):
		n, err := strconv.Atoi(revPattern.FindStringSubmatch(s)[1])
		if err != nil || n < 1 {
			return p, fmt.Errorf("%q is not a version number: versions start at 1", s)
		}
		p.Kind, p.Number = PointVersion, n
	case datePattern.MatchString(s):
		day, err := time.ParseInLocation(time.DateOnly, s, time.Local)
		if err != nil {
			return p, fmt.Errorf("%q is not a date: write it as 2026-09-01", s)
		}
		p.Kind, p.Day = PointDate, day
	case agoPattern.MatchString(s):
		m := agoPattern.FindStringSubmatch(s)
		n, _ := strconv.Atoi(m[1])
		p.Kind, p.Ago = PointAgo, time.Duration(n)*agoUnits[strings.ToLower(m[2])]
	default:
		p.Kind, p.Release = PointRelease, s
	}
	return p, nil
}

var agoUnits = map[string]time.Duration{
	"h":  time.Hour,
	"d":  24 * time.Hour,
	"w":  7 * 24 * time.Hour,
	"mo": 30 * 24 * time.Hour,
	"y":  365 * 24 * time.Hour,
}

// IsRange reports whether an argument is a range: it holds "..".
func IsRange(arg string) bool {
	return strings.Contains(arg, "..")
}

// LooksLikePoint reports whether an argument reads as a point without
// looking anything up: now, a version number, a date or a duration.
func LooksLikePoint(arg string) bool {
	p, err := ParsePoint(arg)
	return err == nil && p.Kind != PointRelease
}

// ParseRange reads "from..to". Either side may be left out: the start is
// then the oldest version kept, the end now. "..." works too.
func ParseRange(arg string) (from, to Point, err error) {
	sep := ".."
	if strings.Contains(arg, "...") {
		sep = "..."
	}
	left, right, ok := strings.Cut(arg, sep)
	if !ok {
		return from, to, fmt.Errorf("%q is not a range: write from..to, such as v1.2.0..v2.0.0, or 30d..now from a point", arg)
	}
	from = Point{Kind: PointOldest}
	if strings.TrimSpace(left) != "" {
		if from, err = ParsePoint(left); err != nil {
			return from, to, err
		}
	}
	to = Point{Raw: "now", Kind: PointNow}
	if strings.TrimSpace(right) != "" {
		if to, err = ParsePoint(right); err != nil {
			return from, to, err
		}
	}
	return from, to, nil
}

// ErrNoVersions is returned when the project has no version to compare.
var ErrNoVersions = errors.New("no versions recorded")

// Sources are what points are found in.
type Sources struct {
	// Versions are the project's versions, oldest first.
	Versions []api.Version
	Releases []api.ReleaseDetail
	Now      time.Time
}

// Resolved is a point found among the versions.
type Resolved struct {
	// Version is nil for now.
	Version *api.Version `json:"version,omitempty"`
	// Label is the point as the reader named it: "v1.2.0", "REV 12",
	// "2026-09-01", "30d ago", "now".
	Label string `json:"label"`
}

// IsNow reports whether the point is the current model.
func (r Resolved) IsNow() bool {
	return r.Version == nil
}

// Number is the version's number, 0 for now.
func (r Resolved) Number() int {
	if r.Version == nil {
		return 0
	}
	return r.Version.Number
}

// String is the label with the version it stands for: "v1.2.0 (REV 12,
// 2026-09-14)", "REV 12 (2026-09-14)", "now".
func (r Resolved) String() string {
	if r.Version == nil {
		return r.Label
	}
	rev := "REV " + strconv.Itoa(r.Version.Number)
	day := r.Version.CreatedAt.Local().Format(time.DateOnly)
	if r.Label == rev {
		return rev + " (" + day + ")"
	}
	return r.Label + " (" + rev + ", " + day + ")"
}

// Resolve finds a point among the versions: a release or a time stands for
// the latest version created at or before it.
func Resolve(p Point, src Sources) (Resolved, error) {
	if p.Kind == PointNow {
		return Resolved{Label: "now"}, nil
	}
	if len(src.Versions) == 0 {
		return Resolved{}, ErrNoVersions
	}
	switch p.Kind {
	case PointOldest:
		return versionPoint(&src.Versions[0], ""), nil
	case PointVersion:
		for i := range src.Versions {
			if src.Versions[i].Number == p.Number {
				return versionPoint(&src.Versions[i], ""), nil
			}
		}
		first, last := src.Versions[0].Number, src.Versions[len(src.Versions)-1].Number
		return Resolved{}, fmt.Errorf("there is no REV %d: Archyl keeps REV %d to REV %d for this project", p.Number, first, last)
	case PointDate:
		end := p.Day.AddDate(0, 0, 1)
		return before(src.Versions, end, p.Raw, p.Raw)
	case PointAgo:
		at := src.Now.Add(-p.Ago)
		return before(src.Versions, at.Add(time.Nanosecond), p.Raw+" ago", p.Raw+" ago ("+at.Local().Format(time.DateOnly)+")")
	}
	rel, err := findRelease(p.Release, src.Releases)
	if err != nil {
		return Resolved{}, err
	}
	when := rel.When()
	return before(src.Versions, when.Add(time.Nanosecond), rel.Version,
		"release "+rel.Version+" ("+when.Local().Format(time.DateOnly)+")")
}

func versionPoint(v *api.Version, label string) Resolved {
	if label == "" {
		label = "REV " + strconv.Itoa(v.Number)
	}
	return Resolved{Version: v, Label: label}
}

// before is the latest version created before end.
func before(
	versions []api.Version,
	end time.Time,
	label string,
	what string,
) (Resolved, error) {
	for i := len(versions) - 1; i >= 0; i-- {
		if versions[i].CreatedAt.Before(end) {
			return versionPoint(&versions[i], label), nil
		}
	}
	oldest := versions[0]
	return Resolved{}, fmt.Errorf("no version at or before %s: the oldest Archyl keeps is REV %d from %s",
		what, oldest.Number, oldest.CreatedAt.Local().Format(time.DateOnly))
}

// findRelease matches a release version exactly, then ignoring case and a
// leading "v". A version released several times stands for its first.
func findRelease(version string, releases []api.ReleaseDetail) (*api.ReleaseDetail, error) {
	var found []api.ReleaseDetail
	for _, r := range releases {
		if r.Version == version {
			found = append(found, r)
		}
	}
	if len(found) == 0 {
		for _, r := range releases {
			if releaseKey(r.Version) == releaseKey(version) {
				found = append(found, r)
			}
		}
	}
	if len(found) == 0 {
		return nil, unknownRelease(version, releases)
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].When().Before(found[j].When()) })
	return &found[0], nil
}

// IsRelease reports whether a release of that version is recorded.
func IsRelease(version string, releases []api.ReleaseDetail) bool {
	_, err := findRelease(version, releases)
	return err == nil
}

func releaseKey(v string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(v)), "v")
}

// unknownRelease says a release is unknown, with the closest names.
func unknownRelease(version string, releases []api.ReleaseDetail) error {
	if len(releases) == 0 {
		return fmt.Errorf("no release %q: none is recorded for this project; record one with archyl release <version>, or name a version (12), a date (2026-09-01) or a duration (30d)", version)
	}
	type candidate struct {
		name     string
		distance int
	}
	seen := map[string]bool{}
	var candidates []candidate
	for _, r := range releases {
		if seen[r.Version] {
			continue
		}
		seen[r.Version] = true
		candidates = append(candidates, candidate{r.Version, levenshtein(releaseKey(version), releaseKey(r.Version))})
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].distance < candidates[j].distance })
	var names []string
	for _, c := range candidates[:min(len(candidates), 3)] {
		names = append(names, c.name)
	}
	return fmt.Errorf("no release %q; the closest are %s. Name one of them, a version (12), a date (2026-09-01) or a duration (30d)",
		version, strings.Join(names, ", "))
}

// levenshtein is the edit distance between two strings, in runes.
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}

// DefaultRange is the range with none given: from the version before the
// latest to now, so the latest edits and anything not yet recorded show.
func DefaultRange(versions []api.Version) (from, to Point) {
	to = Point{Raw: "now", Kind: PointNow}
	if len(versions) < 2 {
		return Point{Kind: PointOldest}, to
	}
	n := versions[len(versions)-2].Number
	return Point{Raw: strconv.Itoa(n), Kind: PointVersion, Number: n}, to
}
