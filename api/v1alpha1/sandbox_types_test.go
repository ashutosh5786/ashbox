package v1alpha1

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
)

func TestAddToScheme(t *testing.T) {
	s := runtime.NewScheme()
	if err := AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"Sandbox", "SandboxList"} {
		if !s.Recognizes(GroupVersion.WithKind(kind)) {
			t.Errorf("scheme does not know %s", kind)
		}
	}
}

func TestDeepCopyIsIndependent(t *testing.T) {
	a := &Sandbox{Spec: SandboxSpec{Forks: []Fork{{ForkOf: WorkloadRef{Name: "cart"}}}}}
	b := a.DeepCopy()
	b.Spec.Forks[0].ForkOf.Name = "other"
	if a.Spec.Forks[0].ForkOf.Name != "cart" {
		t.Fatal("DeepCopy shares the forks slice")
	}
}
