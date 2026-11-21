package controller

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"

	v1alpha1 "github.com/ashutosh5786/ashbox/api/v1alpha1"
	"github.com/ashutosh5786/ashbox/internal/status"
)

var created = time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)

func setup(t *testing.T, now time.Time, sb *v1alpha1.Sandbox, objs ...client.Object) (*SandboxReconciler, client.Client) {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		clientgoscheme.AddToScheme, gatewayv1.Install, gatewayv1beta1.Install, v1alpha1.AddToScheme,
	} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	if sb.UID == "" {
		sb.UID = types.UID("uid-" + sb.Name)
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(append(objs, sb)...).
		WithStatusSubresource(&v1alpha1.Sandbox{}, &appsv1.Deployment{}).
		Build()
	return &SandboxReconciler{
		Client:     c,
		BaggageKey: "routing-key",
		Mesh:       MeshNone,
		Phases:     status.DefaultSettings,
		Now:        func() time.Time { return now },
	}, c
}

func sandbox(name, duration string) *v1alpha1.Sandbox {
	sb := &v1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: name, CreationTimestamp: metav1.NewTime(created)},
	}
	if duration != "" {
		sb.Spec.TTL = &v1alpha1.TTL{Duration: duration}
	}
	return sb
}

func reconcile(t *testing.T, r *SandboxReconciler, name string) ctrl.Result {
	t.Helper()
	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: name}})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestReconcileSetsStatus(t *testing.T) {
	now := created.Add(time.Hour)
	r, c := setup(t, now, sandbox("M1234", "4h"))

	res := reconcile(t, r, "M1234")
	if res.RequeueAfter != 3*time.Hour {
		t.Errorf("requeue = %v, want 3h (until expiry)", res.RequeueAfter)
	}

	var got v1alpha1.Sandbox
	if err := c.Get(context.Background(), types.NamespacedName{Name: "M1234"}, &got); err != nil {
		t.Fatal(err)
	}
	s := got.Status
	// No keySource or routingKey: the key comes from the UID, never the name.
	if s.RoutingKey != v1alpha1.DeriveRoutingKey("uid:uid-M1234") || s.Phase != v1alpha1.PhaseReady {
		t.Errorf("status = %+v", s)
	}
	if s.ExpiresAt == nil || !s.ExpiresAt.Time.Equal(created.Add(4*time.Hour)) {
		t.Errorf("expiresAt = %v", s.ExpiresAt)
	}
}

// A sandbox deleted and created again with the same keySource (for example
// the same pull request) gets the same key, even with a new name and UID.
func TestReconcileKeySourceGivesSameKeyAfterRecreate(t *testing.T) {
	first := sandbox("pr-42", "")
	first.Spec.KeySource = "github.com/acme/shop#42"
	r, c := setup(t, created, first)
	reconcile(t, r, "pr-42")
	k1 := getSandbox(t, c, "pr-42").Status.RoutingKey

	second := sandbox("pr-42-again", "")
	second.Spec.KeySource = "github.com/acme/shop#42"
	r2, c2 := setup(t, created, second)
	reconcile(t, r2, "pr-42-again")
	k2 := getSandbox(t, c2, "pr-42-again").Status.RoutingKey

	if k1 == "" || k1 != k2 || k1 != v1alpha1.DeriveRoutingKey("github.com/acme/shop#42") {
		t.Fatalf("keys %q and %q: want the same derived key", k1, k2)
	}
}

func TestReconcileExplicitRoutingKey(t *testing.T) {
	sb := sandbox("pr-42", "")
	sb.Spec.RoutingKey = "cart-pricing"
	r, c := setup(t, created, sb)
	reconcile(t, r, "pr-42")
	if k := getSandbox(t, c, "pr-42").Status.RoutingKey; k != "cart-pricing" {
		t.Fatalf("key = %q, want cart-pricing", k)
	}
}

// Once assigned, the key in status is kept.
func TestReconcileKeepsAssignedKey(t *testing.T) {
	sb := sandbox("old", "")
	sb.Status.RoutingKey = "assigned-before"
	r, c := setup(t, created, sb)
	reconcile(t, r, "old")
	if k := getSandbox(t, c, "old").Status.RoutingKey; k != "assigned-before" {
		t.Fatalf("key = %q, want assigned-before", k)
	}
}

// Two live sandboxes on one key: the older keeps it, the newer fails and
// checks again later. A sandbox that is being deleted does not count.
func TestReconcileKeyConflict(t *testing.T) {
	older := sandbox("pr-42", "")
	older.Spec.KeySource = "github.com/acme/shop#42"
	newer := sandbox("pr-42-b", "")
	newer.CreationTimestamp = metav1.NewTime(created.Add(time.Minute))
	newer.UID = "uid-b"
	newer.Spec.KeySource = "github.com/acme/shop#42"
	r, c := setup(t, created.Add(time.Hour), newer, older)

	res := reconcile(t, r, "pr-42-b")
	got := getSandbox(t, c, "pr-42-b")
	if got.Status.Phase != v1alpha1.PhaseFailed {
		t.Fatalf("newer phase = %s, want Failed", got.Status.Phase)
	}
	if res.RequeueAfter != keyConflictRetry {
		t.Errorf("requeue = %v, want %v", res.RequeueAfter, keyConflictRetry)
	}

	reconcile(t, r, "pr-42")
	if p := getSandbox(t, c, "pr-42").Status.Phase; p == v1alpha1.PhaseFailed {
		t.Fatalf("older sandbox lost its key")
	}

	// The older one is being deleted: the newer one takes the key.
	o := getSandbox(t, c, "pr-42")
	o.Finalizers = []string{"test/hold"}
	if err := c.Update(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, "pr-42-b")
	if p := getSandbox(t, c, "pr-42-b").Status.Phase; p == v1alpha1.PhaseFailed {
		t.Fatalf("newer sandbox still Failed after the older one started deleting")
	}
}

func TestReconcileDeletesExpired(t *testing.T) {
	r, c := setup(t, created.Add(5*time.Hour), sandbox("m1", "4h"))
	reconcile(t, r, "m1")
	err := c.Get(context.Background(), types.NamespacedName{Name: "m1"}, &v1alpha1.Sandbox{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expired sandbox still there: err = %v", err)
	}
}

func TestReconcileInvalidTTLFails(t *testing.T) {
	r, c := setup(t, created, sandbox("m1", "soon"))
	reconcile(t, r, "m1")
	var got v1alpha1.Sandbox
	_ = c.Get(context.Background(), types.NamespacedName{Name: "m1"}, &got)
	if got.Status.Phase != v1alpha1.PhaseFailed {
		t.Fatalf("phase = %s, want Failed", got.Status.Phase)
	}
}

func TestReconcileNoTTL(t *testing.T) {
	r, _ := setup(t, created, sandbox("m1", ""))
	if res := reconcile(t, r, "m1"); res.RequeueAfter != 0 {
		t.Fatalf("requeue = %v, want none", res.RequeueAfter)
	}
}

func TestReconcileMissing(t *testing.T) {
	r, _ := setup(t, created, sandbox("m1", ""))
	reconcile(t, r, "gone")
}
