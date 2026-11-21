// Package controller reconciles Sandbox objects.
package controller

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	v1alpha1 "github.com/ashutosh5786/ashbox/api/v1alpha1"
	"github.com/ashutosh5786/ashbox/internal/names"
	"github.com/ashutosh5786/ashbox/internal/status"
	"github.com/ashutosh5786/ashbox/internal/ttl"
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
// +kubebuilder:rbac:groups=ashbox.dev,resources=sandboxes/finalizers,verbs=update
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
	var problems []string

	// TODO: track the last spec change for offsetFrom: updatedAt.
	created := sb.CreationTimestamp.Time
	expiresAt, hasTTL, err := ttl.ExpiresAt(sb.Spec.TTL, created, created)
	if err != nil {
		problems = append(problems, err.Error())
	}
	if hasTTL && !now.Before(expiresAt) {
		logger.Info("sandbox expired, deleting", "expiresAt", expiresAt)
		if err := r.Delete(ctx, &sb); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	pr := &progress{keep: map[objKey]bool{}, waypointless: map[string]bool{}}
	if len(sb.Name) > names.MaxNameLength {
		// The name is used as a label value, which is limited to 63 characters.
		problems = append(problems, fmt.Sprintf("sandbox name longer than %d characters", names.MaxNameLength))
	} else {
		if err := r.reconcileForks(ctx, &sb, pr); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.reconcileLocal(ctx, &sb, pr); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.prune(ctx, &sb, pr.keep); err != nil {
			return ctrl.Result{}, err
		}
	}
	problems = append(problems, pr.problems...)

	res := r.Phases.Evaluate(now, sb.Status, pr.obs, len(problems) > 0)

	next := sb.Status.DeepCopy()
	next.Phase = res.Phase
	next.RoutingKey = names.RoutingKey(sb.Name)
	next.Forks = res.Forks
	next.ExpiresAt = nil
	if hasTTL {
		next.ExpiresAt = toMetaTime(expiresAt)
	}
	r.setConditions(next, sb.Generation, problems, pr.waypointless)

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

// progress collects what one reconcile found and created.
type progress struct {
	obs          []status.Observation
	problems     []string        // spec errors: the sandbox is Failed
	keep         map[objKey]bool // children that must exist; others are pruned
	waypointless map[string]bool // routed namespaces without a waypoint
}

func (p *progress) problem(format string, args ...any) {
	p.problems = append(p.problems, fmt.Sprintf(format, args...))
}

// Condition types and reasons.
const (
	ConditionSpecValid = "SpecValid"
	ReasonInvalid      = "Invalid"
	ReasonValid        = "Valid"
	ReasonNoWaypoint   = "NoWaypoint"
	ReasonProgrammed   = "Programmed"
)

func (r *SandboxReconciler) setConditions(s *v1alpha1.SandboxStatus, gen int64, problems []string, waypointless map[string]bool) {
	if len(problems) > 0 {
		meta.SetStatusCondition(&s.Conditions, metav1.Condition{
			Type: ConditionSpecValid, Status: metav1.ConditionFalse, Reason: ReasonInvalid,
			Message: strings.Join(problems, "; "), ObservedGeneration: gen,
		})
	} else {
		meta.SetStatusCondition(&s.Conditions, metav1.Condition{
			Type: ConditionSpecValid, Status: metav1.ConditionTrue, Reason: ReasonValid, ObservedGeneration: gen,
		})
	}

	if r.Mesh != MeshIstio {
		meta.RemoveStatusCondition(&s.Conditions, v1alpha1.ConditionRoutesProgrammed)
		return
	}
	if len(waypointless) > 0 {
		ns := make([]string, 0, len(waypointless))
		for n := range waypointless {
			ns = append(ns, n)
		}
		sort.Strings(ns)
		meta.SetStatusCondition(&s.Conditions, metav1.Condition{
			Type: v1alpha1.ConditionRoutesProgrammed, Status: metav1.ConditionFalse, Reason: ReasonNoWaypoint,
			Message: "no waypoint in namespaces: " + strings.Join(ns, ", "), ObservedGeneration: gen,
		})
		return
	}
	meta.SetStatusCondition(&s.Conditions, metav1.Condition{
		Type: v1alpha1.ConditionRoutesProgrammed, Status: metav1.ConditionTrue, Reason: ReasonProgrammed, ObservedGeneration: gen,
	})
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
