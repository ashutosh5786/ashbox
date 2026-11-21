package controller

import (
	"context"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1alpha1 "ashbox/api/v1alpha1"
	"ashbox/internal/status"
)

var created = time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)

func setup(t *testing.T, now time.Time, sb *v1alpha1.Sandbox) (*SandboxReconciler, client.Client) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(sb).
		WithStatusSubresource(&v1alpha1.Sandbox{}).
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
	if s.RoutingKey != "m1234" || s.Phase != v1alpha1.PhaseReady {
		t.Errorf("status = %+v", s)
	}
	if s.ExpiresAt == nil || !s.ExpiresAt.Time.Equal(created.Add(4*time.Hour)) {
		t.Errorf("expiresAt = %v", s.ExpiresAt)
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
