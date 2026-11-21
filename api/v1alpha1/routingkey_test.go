package v1alpha1

import (
	"regexp"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var derivedRE = regexp.MustCompile(`^[a-z2-7]{12}$`)

func TestDeriveRoutingKeyIsStableAndDistinct(t *testing.T) {
	a := DeriveRoutingKey("github.com/acme/shop#42")
	if !derivedRE.MatchString(a) {
		t.Fatalf("key %q: want 12 chars of [a-z2-7]", a)
	}
	if b := DeriveRoutingKey("github.com/acme/shop#42"); a != b {
		t.Fatalf("same source gave %q and %q", a, b)
	}
	if c := DeriveRoutingKey("github.com/acme/shop#43"); a == c {
		t.Fatalf("different sources gave the same key %q", a)
	}
}

func TestComputeRoutingKeyOrder(t *testing.T) {
	sb := &Sandbox{ObjectMeta: metav1.ObjectMeta{Name: "pr-42"}}
	if k := sb.ComputeRoutingKey(); k != "" {
		t.Fatalf("no UID, no spec: key = %q, want empty", k)
	}
	sb.UID = "u1"
	fromUID := sb.ComputeRoutingKey()
	if !derivedRE.MatchString(fromUID) || fromUID == "pr-42" {
		t.Fatalf("from UID: key = %q", fromUID)
	}
	sb.Spec.KeySource = "github.com/acme/shop#42"
	if k := sb.ComputeRoutingKey(); k != DeriveRoutingKey("github.com/acme/shop#42") {
		t.Fatalf("keySource ignored: %q", k)
	}
	sb.Spec.RoutingKey = "my-own-key"
	if k := sb.ComputeRoutingKey(); k != "my-own-key" {
		t.Fatalf("routingKey ignored: %q", k)
	}
	sb.Status.RoutingKey = "assigned1"
	if k := sb.EffectiveRoutingKey(); k != "assigned1" {
		t.Fatalf("status key not kept: %q", k)
	}
}
