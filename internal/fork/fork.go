// Package fork builds the Deployment and Services of a fork from its baseline.
//
// Rendering is pure: it takes the baseline objects and returns new ones, so
// every rule can be tested without a cluster. The controller reads the
// baseline and applies the result.
package fork

import (
	"encoding/json"
	"fmt"

	jsonpatch "gopkg.in/evanphx/json-patch.v4"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/strategicpatch"

	v1alpha1 "github.com/ashutosh5786/ashbox/api/v1alpha1"
)

// Input is everything needed to render one fork Deployment.
type Input struct {
	Name      string
	Namespace string
	Labels    map[string]string // sandbox labels; also the fork's selector
	Owner     metav1.OwnerReference

	// Template is the baseline pod template.
	Template corev1.PodTemplateSpec
	// DropLabels are pod label keys to remove from the fork: the keys used by
	// the selectors of the baseline Services, so those Services never send
	// traffic to fork pods.
	DropLabels []string

	Customizations *v1alpha1.Customizations
}

// Deployment renders the fork Deployment: one replica, never unavailable
// during an update, selecting only its own pods.
func Deployment(in Input) (*appsv1.Deployment, error) {
	tpl := *in.Template.DeepCopy()

	podLabels := map[string]string{}
	for k, v := range tpl.Labels {
		podLabels[k] = v
	}
	for _, k := range in.DropLabels {
		delete(podLabels, k)
	}
	for k, v := range in.Labels {
		podLabels[k] = v
	}
	tpl.Labels = podLabels

	if c := in.Customizations; c != nil {
		if err := setImages(&tpl.Spec, c.Images); err != nil {
			return nil, err
		}
		setEnv(&tpl.Spec, c.Env)
	}

	one := int32(1)
	zero := intstr.FromInt32(0)
	maxSurge := intstr.FromInt32(1)
	d := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
		ObjectMeta: metav1.ObjectMeta{
			Name:            in.Name,
			Namespace:       in.Namespace,
			Labels:          in.Labels,
			OwnerReferences: []metav1.OwnerReference{in.Owner},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &one,
			Selector: &metav1.LabelSelector{MatchLabels: in.Labels},
			Strategy: appsv1.DeploymentStrategy{
				Type: appsv1.RollingUpdateDeploymentStrategyType,
				RollingUpdate: &appsv1.RollingUpdateDeployment{
					MaxUnavailable: &zero,
					MaxSurge:       &maxSurge,
				},
			},
			Template: tpl,
		},
	}

	if c := in.Customizations; c != nil && c.Patch != nil {
		return applyPatch(d, c.Patch)
	}
	return d, nil
}

func setImages(spec *corev1.PodSpec, images []v1alpha1.ImageOverride) error {
	for _, img := range images {
		found := false
		for i := range spec.Containers {
			if spec.Containers[i].Name == img.Container {
				spec.Containers[i].Image = img.Image
				found = true
			}
		}
		for i := range spec.InitContainers {
			if spec.InitContainers[i].Name == img.Container {
				spec.InitContainers[i].Image = img.Image
				found = true
			}
		}
		if !found {
			return fmt.Errorf("no container %q in baseline", img.Container)
		}
	}
	return nil
}

// setEnv sets each variable on every container, replacing one with the same
// name and keeping the rest in order.
func setEnv(spec *corev1.PodSpec, env []corev1.EnvVar) {
	for i := range spec.Containers {
		c := &spec.Containers[i]
		for _, e := range env {
			replaced := false
			for j := range c.Env {
				if c.Env[j].Name == e.Name {
					c.Env[j] = e
					replaced = true
					break
				}
			}
			if !replaced {
				c.Env = append(c.Env, e)
			}
		}
	}
}

func applyPatch(d *appsv1.Deployment, p *v1alpha1.Patch) (*appsv1.Deployment, error) {
	orig, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	patch := []byte(p.Value)
	if !json.Valid(patch) {
		// Specs are often written as YAML; accept that too.
		if patch, err = yamlToJSON(p.Value); err != nil {
			return nil, fmt.Errorf("patch: %w", err)
		}
	}

	var out []byte
	switch p.Type {
	case "strategic", "":
		out, err = strategicpatch.StrategicMergePatch(orig, patch, appsv1.Deployment{})
	case "merge":
		out, err = jsonpatch.MergePatch(orig, patch)
	case "json":
		var ops jsonpatch.Patch
		if ops, err = jsonpatch.DecodePatch(patch); err == nil {
			out, err = ops.Apply(orig)
		}
	default:
		return nil, fmt.Errorf("unknown patch type %q", p.Type)
	}
	if err != nil {
		return nil, fmt.Errorf("patch: %w", err)
	}

	res := &appsv1.Deployment{}
	if err := json.Unmarshal(out, res); err != nil {
		return nil, fmt.Errorf("patch: %w", err)
	}
	// A patch may not move the fork out of its sandbox.
	res.Name, res.Namespace = d.Name, d.Namespace
	res.OwnerReferences = d.OwnerReferences
	res.Spec.Selector = d.Spec.Selector
	for k, v := range d.Spec.Selector.MatchLabels {
		if res.Spec.Template.Labels == nil {
			res.Spec.Template.Labels = map[string]string{}
		}
		res.Spec.Template.Labels[k] = v
	}
	return res, nil
}

// TemplateFromRollout reads the pod template of an Argo Rollout, read as an
// unstructured object so ashbox does not depend on the Argo Rollouts module.
func TemplateFromRollout(u *unstructured.Unstructured) (corev1.PodTemplateSpec, error) {
	var tpl corev1.PodTemplateSpec
	raw, found, err := unstructured.NestedMap(u.Object, "spec", "template")
	if err != nil {
		return tpl, err
	}
	if !found {
		// TODO: support spec.workloadRef (template held by a Deployment).
		return tpl, fmt.Errorf("rollout %s/%s has no spec.template", u.GetNamespace(), u.GetName())
	}
	err = runtime.DefaultUnstructuredConverter.FromUnstructured(raw, &tpl)
	return tpl, err
}

// Selects reports whether a Service selector picks pods with these labels.
// A Service without a selector selects nothing.
func Selects(selector, podLabels map[string]string) bool {
	if len(selector) == 0 {
		return false
	}
	for k, v := range selector {
		if podLabels[k] != v {
			return false
		}
	}
	return true
}

// DropLabels collects the selector keys of the baseline Services that select
// the baseline pods.
func DropLabels(services []corev1.Service) []string {
	seen := map[string]bool{}
	var keys []string
	for _, s := range services {
		for k := range s.Spec.Selector {
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
	}
	return keys
}

// Service renders the fork copy of a baseline Service: same ports, selecting
// only the fork pods.
func Service(baseline *corev1.Service, name string, labels map[string]string, owner metav1.OwnerReference) *corev1.Service {
	ports := make([]corev1.ServicePort, len(baseline.Spec.Ports))
	for i, p := range baseline.Spec.Ports {
		p.NodePort = 0
		ports[i] = p
	}
	return &corev1.Service{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Service"},
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       baseline.Namespace,
			Labels:          labels,
			OwnerReferences: []metav1.OwnerReference{owner},
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: labels,
			Ports:    ports,
		},
	}
}
