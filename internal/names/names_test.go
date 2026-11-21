package names

import (
	"strings"
	"testing"
)

func TestRoutingKey(t *testing.T) {
	if got := RoutingKey("M1234"); got != "m1234" {
		t.Fatalf("RoutingKey = %q, want m1234", got)
	}
}

func TestChildShort(t *testing.T) {
	if got := Child("cart", "m1234"); got != "cart-m1234" {
		t.Fatalf("Child = %q", got)
	}
}

func TestChildLong(t *testing.T) {
	base := strings.Repeat("a", 70)
	a := Child(base, "m1")
	b := Child(base, "m2")
	if len(a) > MaxNameLength || len(b) > MaxNameLength {
		t.Fatalf("too long: %d, %d", len(a), len(b))
	}
	if a == b {
		t.Fatalf("long names collide: %q", a)
	}
	if a != Child(base, "m1") {
		t.Fatal("Child is not stable")
	}
}

func TestChildNoDashBeforeHash(t *testing.T) {
	// Cut point lands on a dash: the name must not contain "--".
	base := strings.Repeat("a", 53) + "-" + strings.Repeat("b", 20)
	if got := Child(base, "x"); strings.Contains(got, "--") {
		t.Fatalf("Child = %q contains a double dash", got)
	}
}
