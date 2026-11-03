package incident

import (
	"fmt"
	"time"
)

// Ago renders t relative to now: "just now", "12 min ago", "3 h ago",
// "4 d ago", then the date once it is a month old.
func Ago(t, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%d d ago", int(d.Hours()/24))
	}
	return "on " + t.Format("2 Jan 2006")
}
