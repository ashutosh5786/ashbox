package fork

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	v1alpha1 "github.com/ashutosh5786/ashbox/api/v1alpha1"
)

func baselineTemplate() corev1.PodTemplateSpec {
	return corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "cart", "tier": "web", "team": "shop"}},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name:  "app",
			Image: "cart:main",
			Env:   []corev1.EnvVar{{Name: "LOG", Value: "info"}, {Name: "KEEP", Value: "1"}},
		}}},
	}
}

func sandboxLabels() map[string]string {
	return map[string]string{v1alpha1.LabelSandbox: "m1", v1alpha1.LabelFork: "cart"}
}

func TestDeployment(t *testing.T) {
	d, err := Deployment(Input{
		Name: "cart-m1", Namespace: "shop", Labels: sandboxLabels(),
		Template:   baselineTemplate(),
		DropLabels: []string{"app", "tier"},
		Customizations: &v1alpha1.Customizations{
			Images: []v1alpha1.ImageOverride{{Container: "app", Image: "cart:pr1"}},
			Env:    []corev1.EnvVar{{Name: "LOG", Value: "debug"}, {Name: "NEW", Value: "x"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if *d.Spec.Replicas != 1 || d.Spec.Strategy.RollingUpdate.MaxUnavailable.IntValue() != 0 {
		t.Error("want 1 replica and maxUnavailable 0")
	}
	l := d.Spec.Template.Labels
	if _, ok := l["app"]; ok {
		t.Error("baseline selector label app kept: baseline Service would select the fork")
	}
	if l["team"] != "shop" || l[v1alpha1.LabelSandbox] != "m1" {
		t.Errorf("pod labels = %v", l)
	}
	c := d.Spec.Template.Spec.Containers[0]
	if c.Image != "cart:pr1" {
		t.Errorf("image = %q", c.Image)
	}
	want := []string{"LOG=debug", "KEEP=1", "NEW=x"}
	for i, e := range c.Env {
		if e.Name+"="+e.Value != want[i] {
			t.Errorf("env[%d] = %s=%s, want %s", i, e.Name, e.Value, want[i])
		}
	}
}

func TestDeploymentDoesNotChangeBaseline(t *testing.T) {
	tpl := baselineTemplate()
	_, err := Deployment(Input{Labels: sandboxLabels(), Template: tpl, DropLabels: []string{"app"},
		Customizations: &v1alpha1.Customizations{Images: []v1alpha1.ImageOverride{{Container: "app", Image: "x"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if tpl.Labels["app"] != "cart" || tpl.Spec.Containers[0].Image != "cart:main" {
		t.Fatal("baseline template was modified")
	}
}

func TestDeploymentUnknownContainer(t *testing.T) {
	_, err := Deployment(Input{Labels: sandboxLabels(), Template: baselineTemplate(),
		Customizations: &v1alpha1.Customizations{Images: []v1alpha1.ImageOverride{{Container: "nope", Image: "x"}}}})
	if err == nil {
		t.Fatal("want error for unknown container")
	}
}

func TestDeploymentPatch(t *testing.T) {
	patch := `
spec:
  template:
    metadata:
      labels:
        ashbox.dev/sandbox: hijack
    spec:
      containers:
        - name: app
          args: ["--debug"]
`
	d, err := Deployment(Input{Name: "cart-m1", Labels: sandboxLabels(), Template: baselineTemplate(),
		Customizations: &v1alpha1.Customizations{Patch: &v1alpha1.Patch{Type: "strategic", Value: patch}}})
	if err != nil {
		t.Fatal(err)
	}
	c := d.Spec.Template.Spec.Containers[0]
	if len(c.Args) != 1 || c.Args[0] != "--debug" || c.Image != "cart:main" {
		t.Errorf("container after patch = %+v", c)
	}
	if d.Spec.Template.Labels[v1alpha1.LabelSandbox] != "m1" {
		t.Error("patch moved the fork out of its sandbox")
	}
}

func TestDeploymentPatchErrors(t *testing.T) {
	for _, p := range []v1alpha1.Patch{{Type: "strategic", Value: "{not json"}, {Type: "nope", Value: "{}"}} {
		_, err := Deployment(Input{Labels: sandboxLabels(), Template: baselineTemplate(),
			Customizations: &v1alpha1.Customizations{Patch: &p}})
		if err == nil {
			t.Errorf("patch %+v: want error", p)
		}
	}
}

func TestTemplateFromRollout(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{
		"spec": map[string]any{"template": map[string]any{
			"metadata": map[string]any{"labels": map[string]any{"app": "cart"}},
			"spec":     map[string]any{"containers": []any{map[string]any{"name": "app", "image": "cart:main"}}},
		}},
	}}
	tpl, err := TemplateFromRollout(u)
	if err != nil || tpl.Spec.Containers[0].Image != "cart:main" {
		t.Fatalf("tpl = %+v, err = %v", tpl, err)
	}
	if _, err := TemplateFromRollout(&unstructured.Unstructured{Object: map[string]any{}}); err == nil {
		t.Fatal("want error without spec.template")
	}
}

func TestSelectsAndDropLabels(t *testing.T) {
	pod := map[string]string{"app": "cart", "tier": "web"}
	if !Selects(map[string]string{"app": "cart"}, pod) || Selects(map[string]string{"app": "x"}, pod) || Selects(nil, pod) {
		t.Fatal("Selects")
	}
	keys := DropLabels([]corev1.Service{
		{Spec: corev1.ServiceSpec{Selector: map[string]string{"app": "cart"}}},
		{Spec: corev1.ServiceSpec{Selector: map[string]string{"app": "cart", "tier": "web"}}},
	})
	if len(keys) != 2 {
		t.Fatalf("keys = %v", keys)
	}
}

func TestService(t *testing.T) {
	base := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "cart", Namespace: "shop"},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeNodePort,
			Selector: map[string]string{"app": "cart"},
			Ports:    []corev1.ServicePort{{Name: "http", Port: 8080, NodePort: 30001}},
		},
	}
	s := Service(base, "cart-m1", sandboxLabels(), metav1.OwnerReference{})
	if s.Namespace != "shop" || s.Spec.Selector[v1alpha1.LabelSandbox] != "m1" || s.Spec.Selector["app"] != "" {
		t.Errorf("service = %+v", s)
	}
	if s.Spec.Ports[0].NodePort != 0 || s.Spec.Type != corev1.ServiceTypeClusterIP {
		t.Error("fork Service must be a plain ClusterIP")
	}
}
