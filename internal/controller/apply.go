package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"

	v1alpha1 "github.com/ashutosh5786/ashbox/api/v1alpha1"
)

// objKey identifies a child object.
type objKey struct {
	kind, namespace, name string
}

const (
	kindDeployment     = "Deployment"
	kindService        = "Service"
	kindHTTPRoute      = "HTTPRoute"
	kindReferenceGrant = "ReferenceGrant"
)

// ownerRef makes the sandbox the controller of a child. Sandbox is
// cluster-scoped, so children in any namespace may point at it.
func ownerRef(sb *v1alpha1.Sandbox) metav1.OwnerReference {
	return metav1.OwnerReference{
		APIVersion:         v1alpha1.GroupVersion.String(),
		Kind:               "Sandbox",
		Name:               sb.Name,
		UID:                sb.UID,
		Controller:         ptr.To(true),
		BlockOwnerDeletion: ptr.To(true),
	}
}

// claim refuses to take over an existing object that this sandbox does not
// own, for example a real workload that happens to have the fork's name.
func claim(existing client.Object, sb *v1alpha1.Sandbox) error {
	if existing.GetResourceVersion() == "" {
		return nil // being created
	}
	if existing.GetLabels()[v1alpha1.LabelSandbox] != sb.Name {
		return fmt.Errorf("%s/%s exists and does not belong to sandbox %s", existing.GetNamespace(), existing.GetName(), sb.Name)
	}
	return nil
}

func (r *SandboxReconciler) applyDeployment(ctx context.Context, sb *v1alpha1.Sandbox, want *appsv1.Deployment) error {
	got := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: want.Name, Namespace: want.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, got, func() error {
		if err := claim(got, sb); err != nil {
			return err
		}
		got.Labels = want.Labels
		got.OwnerReferences = want.OwnerReferences
		got.Spec = want.Spec
		return nil
	})
	return err
}

func (r *SandboxReconciler) applyService(ctx context.Context, sb *v1alpha1.Sandbox, want *corev1.Service) error {
	got := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: want.Name, Namespace: want.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, got, func() error {
		if err := claim(got, sb); err != nil {
			return err
		}
		got.Labels = want.Labels
		got.OwnerReferences = want.OwnerReferences
		// Only the fields ashbox owns: clusterIP and others are immutable.
		got.Spec.Type = want.Spec.Type
		got.Spec.Selector = want.Spec.Selector
		got.Spec.Ports = want.Spec.Ports
		return nil
	})
	return err
}

func (r *SandboxReconciler) applyRoute(ctx context.Context, sb *v1alpha1.Sandbox, want *gatewayv1.HTTPRoute) error {
	got := &gatewayv1.HTTPRoute{ObjectMeta: metav1.ObjectMeta{Name: want.Name, Namespace: want.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, got, func() error {
		if err := claim(got, sb); err != nil {
			return err
		}
		got.Labels = want.Labels
		got.OwnerReferences = want.OwnerReferences
		got.Spec = want.Spec
		return nil
	})
	return err
}

func (r *SandboxReconciler) applyGrant(ctx context.Context, sb *v1alpha1.Sandbox, want *gatewayv1beta1.ReferenceGrant) error {
	got := &gatewayv1beta1.ReferenceGrant{ObjectMeta: metav1.ObjectMeta{Name: want.Name, Namespace: want.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, got, func() error {
		if err := claim(got, sb); err != nil {
			return err
		}
		got.Labels = want.Labels
		got.OwnerReferences = want.OwnerReferences
		got.Spec = want.Spec
		return nil
	})
	return err
}

// prune deletes children of this sandbox that are no longer wanted, for
// example the objects of a fork removed from the spec.
func (r *SandboxReconciler) prune(ctx context.Context, sb *v1alpha1.Sandbox, keep map[objKey]bool) error {
	sel := client.MatchingLabels{v1alpha1.LabelSandbox: sb.Name}

	var objs []client.Object
	var deps appsv1.DeploymentList
	if err := r.List(ctx, &deps, sel); err != nil {
		return err
	}
	for i := range deps.Items {
		objs = append(objs, &deps.Items[i])
	}
	var svcs corev1.ServiceList
	if err := r.List(ctx, &svcs, sel); err != nil {
		return err
	}
	for i := range svcs.Items {
		objs = append(objs, &svcs.Items[i])
	}
	var routes gatewayv1.HTTPRouteList
	if err := r.List(ctx, &routes, sel); err != nil {
		return err
	}
	for i := range routes.Items {
		objs = append(objs, &routes.Items[i])
	}
	var grants gatewayv1beta1.ReferenceGrantList
	if err := r.List(ctx, &grants, sel); err != nil {
		return err
	}
	for i := range grants.Items {
		objs = append(objs, &grants.Items[i])
	}

	for _, o := range objs {
		if keep[objKey{kindOf(o), o.GetNamespace(), o.GetName()}] || !ownedBy(o, sb) {
			continue
		}
		if err := r.Delete(ctx, o); client.IgnoreNotFound(err) != nil {
			return err
		}
	}
	return nil
}

func kindOf(o client.Object) string {
	switch o.(type) {
	case *appsv1.Deployment:
		return kindDeployment
	case *corev1.Service:
		return kindService
	case *gatewayv1.HTTPRoute:
		return kindHTTPRoute
	case *gatewayv1beta1.ReferenceGrant:
		return kindReferenceGrant
	}
	return ""
}

// ownedBy is true when the sandbox is the object's controller. Pruning only
// touches such objects, never one that merely carries the label.
func ownedBy(o client.Object, sb *v1alpha1.Sandbox) bool {
	for _, ref := range o.GetOwnerReferences() {
		if ref.UID == sb.UID && ref.Controller != nil && *ref.Controller {
			return true
		}
	}
	return false
}
