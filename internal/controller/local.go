package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/ashutosh5786/ashbox/api/v1alpha1"
	"github.com/ashutosh5786/ashbox/internal/names"
	"github.com/ashutosh5786/ashbox/internal/route"
)

// reconcileLocal routes keyed traffic for each spec.local[] baseline to the
// Service that k8s-tunnel published for the laptop.
//
// For each mapping it attaches an HTTPRoute to the baseline Service that has
// that port. When the tunnel Service is in another namespace, it also adds a
// ReferenceGrant there, so the route may use it as a backend.
func (r *SandboxReconciler) reconcileLocal(ctx context.Context, sb *v1alpha1.Sandbox, pr *progress) error {
	owner := ownerRef(sb)
	key := names.RoutingKey(sb.Name)

	for _, l := range sb.Spec.Local {
		labels := names.Labels(sb.Name, l.Name)

		b, err := r.readBaseline(ctx, l.From)
		if err != nil {
			if spec, ok := err.(errSpec); ok {
				pr.problem("local %s: %s", l.Name, spec.msg)
				continue
			}
			return err
		}

		for _, m := range l.Mappings {
			tunnelPort := m.TunnelPort
			if tunnelPort == 0 {
				tunnelPort = m.Port
			}

			matched := false
			for i := range b.services {
				svc := &b.services[i]
				if !hasPort(svc.Spec.Ports, m.Port) {
					continue
				}
				matched = true
				rt := route.Render(route.Target{
					Name: routeName(svc, m.Port, sb.Name), Namespace: svc.Namespace,
					Labels: labels, Owner: owner,
					Service: svc.Name, Port: m.Port,
					BackendNamespace: l.Tunnel.Namespace, BackendService: l.Tunnel.Name, BackendPort: tunnelPort,
				}, r.BaggageKey, key)
				rk := objKey{kindHTTPRoute, rt.Namespace, rt.Name}
				if pr.keep[rk] {
					pr.problem("local %s: %s/%s port %d is already routed by this sandbox", l.Name, svc.Namespace, svc.Name, m.Port)
					continue
				}
				if err := r.applyRoute(ctx, sb, rt); err != nil {
					return fmt.Errorf("local %s: %w", l.Name, err)
				}
				pr.keep[rk] = true

				if l.Tunnel.Namespace != svc.Namespace {
					g := route.RenderReferenceGrant(
						names.Child("ashbox-from-"+svc.Namespace, sb.Name),
						svc.Namespace, l.Tunnel.Namespace, names.Labels(sb.Name, ""), owner)
					if err := r.applyGrant(ctx, sb, g); err != nil {
						return fmt.Errorf("local %s: %w", l.Name, err)
					}
					pr.keep[objKey{kindReferenceGrant, g.Namespace, g.Name}] = true
				}
				if err := r.checkWaypoint(ctx, svc, pr); err != nil {
					return err
				}
			}
			if !matched {
				pr.problem("local %s: no Service of %s/%s has port %d", l.Name, l.From.Namespace, l.From.Name, m.Port)
			}
		}
	}
	return nil
}

func hasPort(ports []corev1.ServicePort, port int32) bool {
	for _, p := range ports {
		if p.Port == port {
			return true
		}
	}
	return false
}
