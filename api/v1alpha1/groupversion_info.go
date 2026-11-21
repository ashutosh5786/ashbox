// Package v1alpha1 holds the ashbox.dev/v1alpha1 API: the Sandbox resource.
//
// It depends only on k8s.io/api and k8s.io/apimachinery, so clients such as
// ashctl can import it without pulling in controller-runtime.
//
// +kubebuilder:object:generate=true
// +groupName=ashbox.dev
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	// GroupVersion is the API group and version of the Sandbox resource.
	GroupVersion = schema.GroupVersion{Group: "ashbox.dev", Version: "v1alpha1"}

	// SchemeBuilder registers the types of this package with a scheme.
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)

	// AddToScheme adds the types of this package to a scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

func addKnownTypes(s *runtime.Scheme) error {
	s.AddKnownTypes(GroupVersion, &Sandbox{}, &SandboxList{})
	metav1.AddToGroupVersion(s, GroupVersion)
	return nil
}
