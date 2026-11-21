package route

import (
	"regexp"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestKeyRegex(t *testing.T) {
	re := regexp.MustCompile(KeyRegex("routing-key", "m1234"))
	match := []string{
		"routing-key=m1234",
		"foo=bar, routing-key=m1234",
		"foo=bar,routing-key=m1234;p=1",
		"routing-key=m1234 , other=x",
	}
	miss := []string{
		"",
		"routing-key=m12",
		"routing-key=m12345",
		"routing-key=xm1234",
		"other-routing-key=m1234",
		"foo=routing-key=m1234",
	}
	for _, h := range match {
		if !re.MatchString(h) {
			t.Errorf("want match: %q", h)
		}
	}
	for _, h := range miss {
		if re.MatchString(h) {
			t.Errorf("want no match: %q", h)
		}
	}
}

func TestKeyRegexQuotesInput(t *testing.T) {
	re := regexp.MustCompile(KeyRegex("k", "a.b"))
	if re.MatchString("k=axb") {
		t.Fatal("dot in key acted as a wildcard")
	}
}

func TestRender(t *testing.T) {
	owner := metav1.OwnerReference{Name: "m1234"}
	r := Render(Target{
		Name: "cart-m1234", Namespace: "shop", Owner: owner,
		Service: "cart", Port: 8080,
		BackendService: "cart-m1234", BackendPort: 8080,
	}, "routing-key", "m1234")

	if got := string(r.Spec.ParentRefs[0].Name); got != "cart" {
		t.Errorf("parent = %q", got)
	}
	b := r.Spec.Rules[0].BackendRefs[0]
	if string(b.Name) != "cart-m1234" || *b.Port != 8080 || b.Namespace != nil {
		t.Errorf("backend = %+v", b.BackendObjectReference)
	}
	if h := r.Spec.Rules[0].Matches[0].Headers[0]; string(h.Name) != HeaderName {
		t.Errorf("header = %q", h.Name)
	}
	if len(r.OwnerReferences) != 1 {
		t.Error("missing owner reference")
	}
}

func TestRenderCrossNamespaceBackend(t *testing.T) {
	r := Render(Target{
		Name: "cart-m1", Namespace: "shop",
		Service: "cart", Port: 8080,
		BackendNamespace: "k8s-tunnel", BackendService: "cart-ash", BackendPort: 8080,
	}, "k", "m1")
	ns := r.Spec.Rules[0].BackendRefs[0].Namespace
	if ns == nil || string(*ns) != "k8s-tunnel" {
		t.Fatalf("backend namespace = %v", ns)
	}

	g := RenderReferenceGrant("ashbox-m1-shop", "shop", "k8s-tunnel", nil, metav1.OwnerReference{})
	if g.Namespace != "k8s-tunnel" || string(g.Spec.From[0].Namespace) != "shop" {
		t.Fatalf("grant = %+v", g)
	}
}
