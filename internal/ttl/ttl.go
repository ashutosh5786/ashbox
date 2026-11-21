// Package ttl parses sandbox lifetimes and computes when a sandbox expires.
package ttl

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	v1alpha1 "ashbox/api/v1alpha1"
)

// Parse reads a duration such as "4h", "90m" or "14d". Days are 24 hours.
func Parse(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("invalid ttl %q", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("invalid ttl %q", s)
	}
	return d, nil
}

// ExpiresAt is when a sandbox expires. ok is false when it has no TTL.
// updated is the time the spec last changed; it is used for offsetFrom
// updatedAt and falls back to created when zero.
func ExpiresAt(spec *v1alpha1.TTL, created, updated time.Time) (at time.Time, ok bool, err error) {
	if spec == nil || spec.Duration == "" {
		return time.Time{}, false, nil
	}
	d, err := Parse(spec.Duration)
	if err != nil {
		return time.Time{}, false, err
	}
	from := created
	if spec.OffsetFrom == v1alpha1.TTLFromUpdated && !updated.IsZero() {
		from = updated
	}
	return from.Add(d), true, nil
}
