package controller

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"

	v1alpha1 "github.com/ashutosh5786/ashbox/api/v1alpha1"
)

// The same shapes as deploy/demo: baseline "echo" in namespace "demo".

func echoBaseline() *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "echo", Namespace: "demo"},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "echo"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "echo", "team": "demo"}},
				Spec: corev1.PodSpec{Containers: []corev1.Container{{
					Name: "app", Image: "hashicorp/http-echo:1.0", Args: []string{"-text=baseline"},
				}}},
			},
		},
	}
}

func echoService(ports ...int32) *corev1.Service {
	if len(ports) == 0 {
		ports = []int32{8080}
	}
	s := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "echo", Namespace: "demo"},
		Spec:       corev1.ServiceSpec{Selector: map[string]string{"app": "echo"}},
	}
	for _, p := range ports {
		s.Spec.Ports = append(s.Spec.Ports, corev1.ServicePort{Port: p})
	}
	return s
}

func otherService() *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "demo"},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "other"},
			Ports:    []corev1.ServicePort{{Port: 80}},
		},
	}
}

func forkSandbox(name string) *v1alpha1.Sandbox {
	sb := sandbox(name, "4h")
	sb.Spec.Forks = []v1alpha1.Fork{{
		ForkOf: v1alpha1.WorkloadRef{Kind: v1alpha1.KindDeployment, Namespace: "demo", Name: "echo"},
		Customizations: &v1alpha1.Customizations{
			Images: []v1alpha1.ImageOverride{{Container: "app", Image: "echo:pr"}},
		},
	}}
	return sb
}

func get[T client.Object](t *testing.T, c client.Client, ns, name string, obj T) T {
	t.Helper()
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, obj); err != nil {
		t.Fatalf("get %s/%s: %v", ns, name, err)
	}
	return obj
}

func gone(t *testing.T, c client.Client, ns, name string, obj client.Object) {
	t.Helper()
	err := c.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, obj)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("%s/%s still exists (err = %v)", ns, name, err)
	}
}

func getSandbox(t *testing.T, c client.Client, name string) *v1alpha1.Sandbox {
	return get(t, c, "", name, &v1alpha1.Sandbox{})
}

func TestForkCreatesChildren(t *testing.T) {
	r, c := setup(t, created, forkSandbox("demo1"), echoBaseline(), echoService(), otherService())
	reconcile(t, r, "demo1")

	d := get(t, c, "demo", "echo-demo1", &appsv1.Deployment{})
	if d.Spec.Template.Spec.Containers[0].Image != "echo:pr" {
		t.Errorf("image = %q", d.Spec.Template.Spec.Containers[0].Image)
	}
	if _, ok := d.Spec.Template.Labels["app"]; ok {
		t.Error("fork pods keep label app: the baseline Service would select them")
	}
	if len(d.OwnerReferences) != 1 || d.OwnerReferences[0].UID != "uid-demo1" {
		t.Errorf("owner = %+v", d.OwnerReferences)
	}

	s := get(t, c, "demo", "echo-demo1", &corev1.Service{})
	if s.Spec.Selector[v1alpha1.LabelSandbox] != "demo1" || s.Spec.Ports[0].Port != 8080 {
		t.Errorf("fork service = %+v", s.Spec)
	}

	rt := get(t, c, "demo", "echo-demo1", &gatewayv1.HTTPRoute{})
	if string(rt.Spec.ParentRefs[0].Name) != "echo" || string(rt.Spec.Rules[0].BackendRefs[0].Name) != "echo-demo1" {
		t.Errorf("route = %+v", rt.Spec)
	}
	if v := rt.Spec.Rules[0].Matches[0].Headers[0].Value; !strings.Contains(v, "routing-key=demo1") {
		t.Errorf("route header regex = %q", v)
	}

	// A Service that does not select the baseline gets no fork.
	gone(t, c, "demo", "other-demo1", &corev1.Service{})

	st := getSandbox(t, c, "demo1").Status
	if st.Phase != v1alpha1.PhasePending || len(st.Forks) != 1 || st.Forks[0].Name != "echo-demo1" {
		t.Fatalf("status = %+v", st)
	}
	if st.Forks[0].Ready || st.Forks[0].Reason != "NoPods" {
		t.Errorf("fork status = %+v", st.Forks[0])
	}
}

func TestForkReadyWhenAvailable(t *testing.T) {
	r, c := setup(t, created, forkSandbox("demo1"), echoBaseline(), echoService())
	reconcile(t, r, "demo1")

	d := get(t, c, "demo", "echo-demo1", &appsv1.Deployment{})
	d.Status.AvailableReplicas = 1
	if err := c.Status().Update(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, "demo1")

	st := getSandbox(t, c, "demo1").Status
	if st.Phase != v1alpha1.PhaseReady || !st.Forks[0].Ready {
		t.Fatalf("status = %+v", st)
	}
}

func TestForkReasonFromPods(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "echo-demo1-x", Namespace: "demo",
			Labels: map[string]string{v1alpha1.LabelSandbox: "demo1", v1alpha1.LabelFork: "echo"}},
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{
			Name: "app", RestartCount: 5,
			State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff", Message: "back-off"}},
		}}},
	}
	r, c := setup(t, created, forkSandbox("demo1"), echoBaseline(), echoService(), pod)
	reconcile(t, r, "demo1")

	f := getSandbox(t, c, "demo1").Status.Forks[0]
	if f.Reason != "CrashLoopBackOff" || !strings.Contains(f.Message, "5 restarts") {
		t.Fatalf("fork = %+v", f)
	}
}

func TestForkMissingBaselineFails(t *testing.T) {
	r, c := setup(t, created, forkSandbox("demo1"))
	reconcile(t, r, "demo1")

	st := getSandbox(t, c, "demo1").Status
	if st.Phase != v1alpha1.PhaseFailed {
		t.Fatalf("phase = %s", st.Phase)
	}
	cond := meta.FindStatusCondition(st.Conditions, ConditionSpecValid)
	if cond == nil || cond.Status != metav1.ConditionFalse || !strings.Contains(cond.Message, "not found") {
		t.Fatalf("condition = %+v", cond)
	}
}

func TestForkBadImageContainerFails(t *testing.T) {
	sb := forkSandbox("demo1")
	sb.Spec.Forks[0].Customizations.Images[0].Container = "nope"
	r, c := setup(t, created, sb, echoBaseline(), echoService())
	reconcile(t, r, "demo1")
	if p := getSandbox(t, c, "demo1").Status.Phase; p != v1alpha1.PhaseFailed {
		t.Fatalf("phase = %s", p)
	}
	gone(t, c, "demo", "echo-demo1", &appsv1.Deployment{})
}

func TestForkRemovedIsPruned(t *testing.T) {
	r, c := setup(t, created, forkSandbox("demo1"), echoBaseline(), echoService())
	reconcile(t, r, "demo1")
	get(t, c, "demo", "echo-demo1", &appsv1.Deployment{})

	sb := getSandbox(t, c, "demo1")
	sb.Spec.Forks = nil
	if err := c.Update(context.Background(), sb); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, "demo1")

	gone(t, c, "demo", "echo-demo1", &appsv1.Deployment{})
	gone(t, c, "demo", "echo-demo1", &corev1.Service{})
	gone(t, c, "demo", "echo-demo1", &gatewayv1.HTTPRoute{})
	// The baseline is never touched.
	get(t, c, "demo", "echo", &appsv1.Deployment{})
	get(t, c, "demo", "echo", &corev1.Service{})
}

func TestPruneSkipsObjectsNotOwned(t *testing.T) {
	// Carries the label, but another owner: must survive.
	stray := &corev1.Service{ObjectMeta: metav1.ObjectMeta{
		Name: "stray", Namespace: "demo", Labels: map[string]string{v1alpha1.LabelSandbox: "demo1"},
	}}
	r, c := setup(t, created, sandbox("demo1", ""), stray)
	reconcile(t, r, "demo1")
	get(t, c, "demo", "stray", &corev1.Service{})
}

func TestForkRefusesTakeover(t *testing.T) {
	existing := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "echo-demo1", Namespace: "demo"}}
	r, c := setup(t, created, forkSandbox("demo1"), echoBaseline(), echoService(), existing)

	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "demo1"}})
	if err == nil || !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("err = %v, want refusal", err)
	}
	if d := get(t, c, "demo", "echo-demo1", &appsv1.Deployment{}); len(d.OwnerReferences) != 0 {
		t.Fatal("existing Deployment was taken over")
	}
}

func TestForkMultiPortRoutes(t *testing.T) {
	r, c := setup(t, created, forkSandbox("demo1"), echoBaseline(), echoService(8080, 9090))
	reconcile(t, r, "demo1")
	get(t, c, "demo", "echo-8080-demo1", &gatewayv1.HTTPRoute{})
	rt := get(t, c, "demo", "echo-9090-demo1", &gatewayv1.HTTPRoute{})
	if *rt.Spec.ParentRefs[0].Port != 9090 || *rt.Spec.Rules[0].BackendRefs[0].Port != 9090 {
		t.Fatalf("route ports = %+v", rt.Spec)
	}
}

func TestForkRollout(t *testing.T) {
	sb := forkSandbox("demo1")
	sb.Spec.Forks[0].ForkOf.Kind = v1alpha1.KindRollout
	r, c := setup(t, created, sb, echoService())
	reconcile(t, r, "demo1")
	// No Rollout in the fake cluster: reported as a spec problem, not a crash.
	if p := getSandbox(t, c, "demo1").Status.Phase; p != v1alpha1.PhaseFailed {
		t.Fatalf("phase = %s", p)
	}
}

func TestIstioWaypointCheck(t *testing.T) {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "demo"}}
	r, c := setup(t, created, forkSandbox("demo1"), echoBaseline(), echoService(), ns)
	r.Mesh = MeshIstio
	reconcile(t, r, "demo1")

	cond := meta.FindStatusCondition(getSandbox(t, c, "demo1").Status.Conditions, v1alpha1.ConditionRoutesProgrammed)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != ReasonNoWaypoint {
		t.Fatalf("condition = %+v", cond)
	}

	ns = get(t, c, "", "demo", &corev1.Namespace{})
	ns.Labels = map[string]string{istioUseWaypoint: "waypoint"}
	if err := c.Update(context.Background(), ns); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, "demo1")
	cond = meta.FindStatusCondition(getSandbox(t, c, "demo1").Status.Conditions, v1alpha1.ConditionRoutesProgrammed)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("condition = %+v", cond)
	}
}

func TestNoMeshHasNoRoutesCondition(t *testing.T) {
	r, c := setup(t, created, forkSandbox("demo1"), echoBaseline(), echoService())
	reconcile(t, r, "demo1")
	if meta.FindStatusCondition(getSandbox(t, c, "demo1").Status.Conditions, v1alpha1.ConditionRoutesProgrammed) != nil {
		t.Fatal("RoutesProgrammed set without --mesh=istio")
	}
}

func TestLongNameFails(t *testing.T) {
	name := strings.Repeat("a", 64)
	r, c := setup(t, created, sandbox(name, ""))
	reconcile(t, r, name)
	if p := getSandbox(t, c, name).Status.Phase; p != v1alpha1.PhaseFailed {
		t.Fatalf("phase = %s", p)
	}
}

func localSandbox(name string, port int32) *v1alpha1.Sandbox {
	sb := sandbox(name, "")
	sb.Spec.Local = []v1alpha1.Local{{
		Name:     "echo-local",
		From:     v1alpha1.WorkloadRef{Kind: v1alpha1.KindDeployment, Namespace: "demo", Name: "echo"},
		Tunnel:   v1alpha1.ServiceRef{Namespace: "k8s-tunnel", Name: "echo-ash"},
		Mappings: []v1alpha1.LocalMapping{{Port: port, TunnelPort: 18080}},
	}}
	return sb
}

func TestLocalRoutesToTunnel(t *testing.T) {
	r, c := setup(t, created, localSandbox("ash1", 8080), echoBaseline(), echoService())
	reconcile(t, r, "ash1")

	rt := get(t, c, "demo", "echo-ash1", &gatewayv1.HTTPRoute{})
	b := rt.Spec.Rules[0].BackendRefs[0]
	if string(b.Name) != "echo-ash" || b.Namespace == nil || string(*b.Namespace) != "k8s-tunnel" || *b.Port != 18080 {
		t.Fatalf("backend = %+v", b.BackendObjectReference)
	}

	g := get(t, c, "k8s-tunnel", "ashbox-from-demo-ash1", &gatewayv1beta1.ReferenceGrant{})
	if string(g.Spec.From[0].Namespace) != "demo" || g.Spec.To[0].Kind != "Service" {
		t.Fatalf("grant = %+v", g.Spec)
	}

	// No fork Deployment for a local entry.
	gone(t, c, "demo", "echo-ash1", &appsv1.Deployment{})
	if p := getSandbox(t, c, "ash1").Status.Phase; p != v1alpha1.PhaseReady {
		t.Fatalf("phase = %s", p)
	}

	// Removing the local entry removes its route and grant.
	sb := getSandbox(t, c, "ash1")
	sb.Spec.Local = nil
	if err := c.Update(context.Background(), sb); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, "ash1")
	gone(t, c, "demo", "echo-ash1", &gatewayv1.HTTPRoute{})
	gone(t, c, "k8s-tunnel", "ashbox-from-demo-ash1", &gatewayv1beta1.ReferenceGrant{})
}

func TestLocalUnknownPortFails(t *testing.T) {
	r, c := setup(t, created, localSandbox("ash1", 9999), echoBaseline(), echoService())
	reconcile(t, r, "ash1")
	st := getSandbox(t, c, "ash1").Status
	cond := meta.FindStatusCondition(st.Conditions, ConditionSpecValid)
	if st.Phase != v1alpha1.PhaseFailed || cond == nil || !strings.Contains(cond.Message, "port 9999") {
		t.Fatalf("status = %+v", st)
	}
}

func TestLocalAndForkOnSameServiceConflict(t *testing.T) {
	sb := localSandbox("x1", 8080)
	sb.Spec.Forks = forkSandbox("x1").Spec.Forks
	r, c := setup(t, created, sb, echoBaseline(), echoService())
	reconcile(t, r, "x1")

	// The fork's route stays; the local entry is reported.
	rt := get(t, c, "demo", "echo-x1", &gatewayv1.HTTPRoute{})
	if string(rt.Spec.Rules[0].BackendRefs[0].Name) != "echo-x1" {
		t.Fatalf("route backend = %s", rt.Spec.Rules[0].BackendRefs[0].Name)
	}
	if p := getSandbox(t, c, "x1").Status.Phase; p != v1alpha1.PhaseFailed && p != v1alpha1.PhasePending {
		t.Fatalf("phase = %s", p)
	}
	cond := meta.FindStatusCondition(getSandbox(t, c, "x1").Status.Conditions, ConditionSpecValid)
	if cond == nil || cond.Status != metav1.ConditionFalse {
		t.Fatalf("condition = %+v", cond)
	}
}

func TestPodProblem(t *testing.T) {
	if r, _ := podProblem(nil); r != "NoPods" {
		t.Errorf("no pods: %s", r)
	}
	unsched := corev1.Pod{Status: corev1.PodStatus{Conditions: []corev1.PodCondition{{
		Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Message: "0/2 nodes",
	}}}}
	if r, m := podProblem([]corev1.Pod{unsched}); r != "Unschedulable" || m != "0/2 nodes" {
		t.Errorf("unschedulable: %s %s", r, m)
	}
	creating := corev1.Pod{Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{
		State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ContainerCreating"}},
	}}}}
	if r, _ := podProblem([]corev1.Pod{creating}); r != "NotReady" {
		t.Errorf("creating: %s", r)
	}
}
