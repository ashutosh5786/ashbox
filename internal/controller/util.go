package controller

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// toMetaTime truncates to seconds, the precision Kubernetes stores, so a
// stored value compares equal to a freshly computed one.
func toMetaTime(t time.Time) *metav1.Time {
	m := metav1.NewTime(t.Truncate(time.Second))
	return &m
}
