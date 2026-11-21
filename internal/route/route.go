// Package route builds the Gateway API objects that send keyed requests to a
// sandbox.
//
// One HTTPRoute is attached to each baseline Service port. Every sandbox adds
// its own route, matching only its own key, so routes of different sandboxes
// never conflict and nobody edits the baseline's objects.
//
// Each route also has a fallback rule with no match that sends requests to
// the baseline Service itself. Some meshes (Istio among them) answer 404 for
// a request that no rule matches once any route is attached to a Service, so
// without the fallback every Service would need a hand-written catch-all
// route. Gateway API precedence ranks a rule with header matches above one
// without, so keyed requests still reach the sandbox. The fallback rules of
// several sandboxes tie, and they all point at the same baseline, so the
// tie does not matter. A baseline route with more specific rules (paths,
// headers) still wins for its requests.
package route

import (
	"regexp"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
)

// HeaderName is the header that carries the routing key (W3C baggage).
const HeaderName = "baggage"

// KeyRegex matches a baggage header that holds exactly "{baggageKey}={key}",
// anywhere in the list and with or without properties. It is anchored on both
// sides, so key m12 never matches m1234.
func KeyRegex(baggageKey, key string) string {
	return `(^|.*,)\s*` + regexp.QuoteMeta(baggageKey) + `=` + regexp.QuoteMeta(key) + `\s*([,;].*)?$`
}

// Target is one baseline Service port and where keyed requests for it go.
type Target struct {
	Name      string // name of the HTTPRoute
	Namespace string // namespace of the baseline Service and the route
	Labels    map[string]string
	Owner     metav1.OwnerReference

	Service string // baseline Service
	Port    int32  // baseline Service port

	BackendNamespace string // empty means Namespace
	BackendService   string
	BackendPort      int32
}

// Render builds the HTTPRoute for a target.
func Render(t Target, baggageKey, key string) *gatewayv1.HTTPRoute {
	core := gatewayv1.Group("")
	svcKind := gatewayv1.Kind("Service")
	port := t.Port
	backendPort := t.BackendPort
	regex := gatewayv1.HeaderMatchRegularExpression

	backend := gatewayv1.BackendObjectReference{
		Group: &core,
		Kind:  &svcKind,
		Name:  gatewayv1.ObjectName(t.BackendService),
		Port:  &backendPort,
	}
	if t.BackendNamespace != "" && t.BackendNamespace != t.Namespace {
		ns := gatewayv1.Namespace(t.BackendNamespace)
		backend.Namespace = &ns
	}
	// The fallback goes to the parent Service: the normal endpoints.
	fallbackPort := t.Port
	fallback := gatewayv1.BackendObjectReference{
		Group: &core,
		Kind:  &svcKind,
		Name:  gatewayv1.ObjectName(t.Service),
		Port:  &fallbackPort,
	}

	return &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:            t.Name,
			Namespace:       t.Namespace,
			Labels:          t.Labels,
			OwnerReferences: []metav1.OwnerReference{t.Owner},
		},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{{
					Group: &core,
					Kind:  &svcKind,
					Name:  gatewayv1.ObjectName(t.Service),
					Port:  &port,
				}},
			},
			Rules: []gatewayv1.HTTPRouteRule{
				{
					Matches: []gatewayv1.HTTPRouteMatch{{
						Headers: []gatewayv1.HTTPHeaderMatch{{
							Type:  &regex,
							Name:  HeaderName,
							Value: KeyRegex(baggageKey, key),
						}},
					}},
					BackendRefs: []gatewayv1.HTTPBackendRef{{
						BackendRef: gatewayv1.BackendRef{BackendObjectReference: backend},
					}},
				},
				{
					// No match: every other request goes to the baseline.
					BackendRefs: []gatewayv1.HTTPBackendRef{{
						BackendRef: gatewayv1.BackendRef{BackendObjectReference: fallback},
					}},
				},
			},
		},
	}
}

// RenderReferenceGrant allows HTTPRoutes in fromNamespace to send traffic to
// Services in toNamespace. A local entry needs it, because its routes live in
// the baseline's namespace and its backend is the k8s-tunnel Service.
func RenderReferenceGrant(name, fromNamespace, toNamespace string, labels map[string]string, owner metav1.OwnerReference) *gatewayv1beta1.ReferenceGrant {
	return &gatewayv1beta1.ReferenceGrant{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       toNamespace,
			Labels:          labels,
			OwnerReferences: []metav1.OwnerReference{owner},
		},
		Spec: gatewayv1beta1.ReferenceGrantSpec{
			From: []gatewayv1beta1.ReferenceGrantFrom{{
				Group:     gatewayv1.GroupName,
				Kind:      "HTTPRoute",
				Namespace: gatewayv1.Namespace(fromNamespace),
			}},
			To: []gatewayv1beta1.ReferenceGrantTo{{
				Group: "",
				Kind:  "Service",
			}},
		},
	}
}
