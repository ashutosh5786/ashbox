package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/ashutosh5786/ashbox/api/v1alpha1"
	"github.com/ashutosh5786/ashbox/internal/fork"
	"github.com/ashutosh5786/ashbox/internal/names"
	"github.com/ashutosh5786/ashbox/internal/route"
	"github.com/ashutosh5786/ashbox/internal/status"
)

// Label on a namespace or Service that names its Istio waypoint.
const istioUseWaypoint = "istio.io/use-waypoint"

var rolloutGVK = schema.GroupVersionKind{Group: "argoproj.io", Version: "v1alpha1", Kind: "Rollout"}

// errSpec marks an error in the sandbox spec: retrying cannot fix it.
type errSpec struct{ msg string }

func (e errSpec) Error() string { return e.msg }

// baseline is a workload a fork or local entry is based on.
type baseline struct {
	template corev1.PodTemplateSpec
	services []corev1.Service // Services in its namespace that select its pods
}

// readBaseline reads a baseline workload and the Services that select it.
func (r *SandboxReconciler) readBaseline(ctx context.Context, ref v1alpha1.WorkloadRef) (*baseline, error) {
	key := client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}
	b := &baseline{}

	switch ref.Kind {
	case v1alpha1.KindDeployment, "":
		var d appsv1.Deployment
		if err := r.Get(ctx, key, &d); err != nil {
			if apierrors.IsNotFound(err) {
				return nil, errSpec{fmt.Sprintf("baseline Deployment %s not found", key)}
			}
			return nil, err
		}
		b.template = d.Spec.Template
	case v1alpha1.KindRollout:
		u := &unstructured.Unstructured{}
		u.SetGroupVersionKind(rolloutGVK)
		if err := r.Get(ctx, key, u); err != nil {
			if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
				return nil, errSpec{fmt.Sprintf("baseline Rollout %s not found", key)}
			}
			return nil, err
		}
		tpl, err := fork.TemplateFromRollout(u)
		if err != nil {
			return nil, errSpec{err.Error()}
		}
		b.template = tpl
	default:
		return nil, errSpec{fmt.Sprintf("unknown workload kind %q", ref.Kind)}
	}

	var svcs corev1.ServiceList
	if err := r.List(ctx, &svcs, client.InNamespace(ref.Namespace)); err != nil {
		return nil, err
	}
	for _, s := range svcs.Items {
		if _, ours := s.Labels[v1alpha1.LabelSandbox]; ours {
			continue
		}
		if fork.Selects(s.Spec.Selector, b.template.Labels) {
			b.services = append(b.services, s)
		}
	}
	return b, nil
}

// routeName is the HTTPRoute name for one baseline Service port. Services
// with one port keep the plain "{service}-{sandbox}" name.
func routeName(svc *corev1.Service, port int32, sandbox string) string {
	if len(svc.Spec.Ports) == 1 {
		return names.Child(svc.Name, sandbox)
	}
	return names.Child(fmt.Sprintf("%s-%d", svc.Name, port), sandbox)
}

// reconcileForks creates or updates each fork's Deployment, Services and
// HTTPRoutes and observes each fork's readiness.
func (r *SandboxReconciler) reconcileForks(ctx context.Context, sb *v1alpha1.Sandbox, pr *progress) error {
	owner := ownerRef(sb)
	key := sb.EffectiveRoutingKey()

	for _, f := range sb.Spec.Forks {
		ref := f.ForkOf
		forkName := names.Child(ref.Name, sb.Name)
		labels := names.Labels(sb.Name, ref.Name)
		obs := status.Observation{Name: forkName}

		b, err := r.readBaseline(ctx, ref)
		if err != nil {
			if spec, ok := err.(errSpec); ok {
				pr.problem("fork %s: %s", ref.Name, spec.msg)
				obs.Reason, obs.Message = "InvalidSpec", spec.msg
				pr.obs = append(pr.obs, obs)
				continue
			}
			return err
		}

		dep, err := fork.Deployment(fork.Input{
			Name: forkName, Namespace: ref.Namespace, Labels: labels, Owner: owner,
			Template:       b.template,
			DropLabels:     fork.DropLabels(b.services),
			Customizations: f.Customizations,
		})
		if err != nil {
			pr.problem("fork %s: %v", ref.Name, err)
			obs.Reason, obs.Message = "InvalidSpec", err.Error()
			pr.obs = append(pr.obs, obs)
			continue
		}
		if err := r.applyDeployment(ctx, sb, dep); err != nil {
			return fmt.Errorf("fork %s: %w", ref.Name, err)
		}
		pr.keep[objKey{kindDeployment, dep.Namespace, dep.Name}] = true

		for i := range b.services {
			svc := &b.services[i]
			forkSvc := fork.Service(svc, names.Child(svc.Name, sb.Name), labels, owner)
			if err := r.applyService(ctx, sb, forkSvc); err != nil {
				return fmt.Errorf("fork %s: %w", ref.Name, err)
			}
			pr.keep[objKey{kindService, forkSvc.Namespace, forkSvc.Name}] = true

			for _, p := range svc.Spec.Ports {
				rt := route.Render(route.Target{
					Name: routeName(svc, p.Port, sb.Name), Namespace: svc.Namespace,
					Labels: labels, Owner: owner,
					Service: svc.Name, Port: p.Port,
					BackendService: forkSvc.Name, BackendPort: p.Port,
				}, r.BaggageKey, key)
				if err := r.applyRoute(ctx, sb, rt); err != nil {
					return fmt.Errorf("fork %s: %w", ref.Name, err)
				}
				pr.keep[objKey{kindHTTPRoute, rt.Namespace, rt.Name}] = true
			}
			if err := r.checkWaypoint(ctx, svc, pr); err != nil {
				return err
			}
		}

		if err := r.observeFork(ctx, dep, &obs); err != nil {
			return err
		}
		pr.obs = append(pr.obs, obs)
	}
	return nil
}

// observeFork sets the fork's readiness from its Deployment, and a reason
// from its pods (for example CrashLoopBackOff) when it is not ready.
func (r *SandboxReconciler) observeFork(ctx context.Context, want *appsv1.Deployment, obs *status.Observation) error {
	var d appsv1.Deployment
	if err := r.Get(ctx, client.ObjectKeyFromObject(want), &d); err != nil {
		return client.IgnoreNotFound(err)
	}
	if d.Status.AvailableReplicas >= 1 {
		obs.Ready = true
		return nil
	}

	var pods corev1.PodList
	if err := r.List(ctx, &pods, client.InNamespace(d.Namespace), client.MatchingLabels(want.Spec.Selector.MatchLabels)); err != nil {
		return err
	}
	obs.Reason, obs.Message = podProblem(pods.Items)
	return nil
}

// podProblem gives the most useful reason why fork pods are not ready.
func podProblem(pods []corev1.Pod) (reason, message string) {
	if len(pods) == 0 {
		return "NoPods", "no pods created yet"
	}
	for _, p := range pods {
		for _, cs := range append(p.Status.InitContainerStatuses, p.Status.ContainerStatuses...) {
			if w := cs.State.Waiting; w != nil && w.Reason != "" && w.Reason != "ContainerCreating" && w.Reason != "PodInitializing" {
				msg := w.Message
				if cs.RestartCount > 0 {
					msg = fmt.Sprintf("%s (%d restarts)", msg, cs.RestartCount)
				}
				return w.Reason, msg
			}
		}
		for _, c := range p.Status.Conditions {
			if c.Type == corev1.PodScheduled && c.Status == corev1.ConditionFalse {
				return "Unschedulable", c.Message
			}
		}
	}
	return "NotReady", "pods are starting or failing their readiness probe"
}

// checkWaypoint records namespaces that have no Istio waypoint, when the
// istio mesh checks are on. A waypoint can be set on the Service or its
// namespace.
func (r *SandboxReconciler) checkWaypoint(ctx context.Context, svc *corev1.Service, pr *progress) error {
	if r.Mesh != MeshIstio {
		return nil
	}
	if svc.Labels[istioUseWaypoint] != "" {
		return nil
	}
	var ns corev1.Namespace
	if err := r.Get(ctx, client.ObjectKey{Name: svc.Namespace}, &ns); err != nil {
		return client.IgnoreNotFound(err)
	}
	if ns.Labels[istioUseWaypoint] == "" {
		pr.waypointless[svc.Namespace] = true
	}
	return nil
}
