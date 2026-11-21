// Package controller reconciles Sandbox objects.
package controller

import (
	"context"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	v1alpha1 "ashbox/api/v1alpha1"
	"ashbox/internal/names"
	"ashbox/internal/status"
	"ashbox/internal/ttl"
)

// Mesh selects mesh-specific checks. Routing itself is plain Gateway API
// (GAMMA: HTTPRoutes attached to Services), so it works on any mesh that
// supports it.
type Mesh string

const (
	// MeshNone does no mesh-specific checks.
	MeshNone Mesh = "none"
	// MeshIstio checks that each routed namespace has an Istio waypoint;
	// without one, routes exist but nothing enforces them.
	MeshIstio Mesh = "istio"
)

// SandboxReconciler creates the forks, Services and routes of a sandbox,
// reports its status and deletes it when its TTL runs out.
type SandboxReconciler struct {
	client.Client

	// BaggageKey is the baggage entry that carries the routing key.
	BaggageKey string
	Mesh       Mesh
	Phases     status.Settings
	// Now returns the current time; tests replace it.
	Now func() time.Time
}

// +kubebuilder:rbac:groups=ashbox.dev,resources=sandboxes,verbs=get;list;watch;update;patch;delete
// +kubebuilder:rbac:groups=ashbox.dev,resources=sandboxes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=argoproj.io,resources=rollouts,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods;namespaces,verbs=get;list;watch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes;referencegrants,verbs=get;list;watch;create;update;patch;delete

// Reconcile brings one sandbox to its wanted state.
func (r *SandboxReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var sb v1alpha1.Sandbox
	if err := r.Get(ctx, req.NamespacedName, &sb); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !sb.DeletionTimestamp.IsZero() {
		// Children go with garbage collection (ownerReferences).
		// TODO: finalizer for reporting the final state (for example to a PR).
		return ctrl.Result{}, nil
	}

	now := r.now()
	invalid := false

	// TODO: track the last spec change for offsetFrom: updatedAt.
	created := sb.CreationTimestamp.Time
	expiresAt, hasTTL, err := ttl.ExpiresAt(sb.Spec.TTL, created, created)
	if err != nil {
		logger.Info("invalid ttl", "error", err.Error())
		invalid = true
	}
	if hasTTL && !now.Before(expiresAt) {
		logger.Info("sandbox expired, deleting", "expiresAt", expiresAt)
		if err := r.Delete(ctx, &sb); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	obs, forksInvalid, err := r.reconcileForks(ctx, &sb)
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := r.reconcileLocal(ctx, &sb); err != nil {
		return ctrl.Result{}, err
	}

	res := r.Phases.Evaluate(now, sb.Status, obs, invalid || forksInvalid)

	next := sb.Status.DeepCopy()
	next.Phase = res.Phase
	next.RoutingKey = names.RoutingKey(sb.Name)
	next.Forks = res.Forks
	if hasTTL {
		t := expiresAt
		next.ExpiresAt = toMetaTime(t)
	} else {
		next.ExpiresAt = nil
	}
	if !equality.Semantic.DeepEqual(&sb.Status, next) {
		sb.Status = *next
		if err := r.Status().Update(ctx, &sb); err != nil {
			return ctrl.Result{}, err
		}
	}

	requeue := res.Requeue
	if hasTTL {
		if untilExpiry := expiresAt.Sub(now); requeue == 0 || untilExpiry < requeue {
			requeue = untilExpiry
		}
	}
	return ctrl.Result{RequeueAfter: requeue}, nil
}

// reconcileForks creates or updates each fork's Deployment, Services and
// HTTPRoutes, removes children of forks no longer in the spec, and reports
// each fork's readiness. invalid is true when a fork can never work (unknown
// baseline, bad patch).
//
// TODO: for each spec.forks[] entry:
//  1. read the baseline (Deployment, or Rollout via fork.TemplateFromRollout);
//  2. list Services in its namespace that select the baseline pods (fork.Selects);
//  3. apply fork.Deployment with DropLabels = fork.DropLabels(those Services);
//  4. apply fork.Service and one route.Render per Service port;
//  5. with MeshIstio, set RoutesProgrammed=False/NoWaypoint when the
//     namespace has no istio.io/use-waypoint label;
//  6. observe readiness from the fork Deployment and its pods.
func (r *SandboxReconciler) reconcileForks(ctx context.Context, sb *v1alpha1.Sandbox) ([]status.Observation, bool, error) {
	return nil, false, nil
}

// reconcileLocal routes keyed traffic for each spec.local[] baseline to the
// Service that k8s-tunnel published for the laptop.
//
// TODO: for each local entry, one route.Render per baseline Service and
// mapping (backend in the tunnel namespace), plus one
// route.RenderReferenceGrant in the tunnel namespace.
func (r *SandboxReconciler) reconcileLocal(ctx context.Context, sb *v1alpha1.Sandbox) error {
	return nil
}

func (r *SandboxReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// SetupWithManager registers the controller. Changes to owned children
// trigger a reconcile of their sandbox.
func (r *SandboxReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Sandbox{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&gatewayv1.HTTPRoute{}).
		Named("sandbox").
		Complete(r)
}
