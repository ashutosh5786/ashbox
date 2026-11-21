package ttl

import (
	"testing"
	"time"

	v1alpha1 "ashbox/api/v1alpha1"
)

func TestParse(t *testing.T) {
	cases := map[string]time.Duration{
		"4h":  4 * time.Hour,
		"90m": 90 * time.Minute,
		"14d": 14 * 24 * time.Hour,
	}
	for in, want := range cases {
		got, err := Parse(in)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "0d", "-1h", "xd", "soon"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) succeeded, want error", bad)
		}
	}
}

func TestExpiresAt(t *testing.T) {
	created := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	updated := created.Add(3 * time.Hour)

	if _, ok, err := ExpiresAt(nil, created, updated); ok || err != nil {
		t.Fatalf("nil ttl: ok=%v err=%v", ok, err)
	}

	at, ok, err := ExpiresAt(&v1alpha1.TTL{Duration: "4h"}, created, updated)
	if err != nil || !ok || !at.Equal(created.Add(4*time.Hour)) {
		t.Fatalf("createdAt: %v %v %v", at, ok, err)
	}

	at, _, _ = ExpiresAt(&v1alpha1.TTL{Duration: "4h", OffsetFrom: v1alpha1.TTLFromUpdated}, created, updated)
	if !at.Equal(updated.Add(4 * time.Hour)) {
		t.Fatalf("updatedAt: %v", at)
	}

	if _, _, err := ExpiresAt(&v1alpha1.TTL{Duration: "nope"}, created, updated); err == nil {
		t.Fatal("bad duration: want error")
	}
}
