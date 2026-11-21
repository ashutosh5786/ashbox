// Package names builds the names and labels of the objects a sandbox owns.
package names

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	v1alpha1 "github.com/ashutosh5786/ashbox/api/v1alpha1"
)

// MaxNameLength is the longest name a Service may have (a DNS-1035 label).
// Deployments and HTTPRoutes allow more; one limit keeps all children aligned.
const MaxNameLength = 63

// RoutingKey is the key carried in the baggage header: the sandbox name in
// lower case.
func RoutingKey(sandbox string) string {
	return strings.ToLower(sandbox)
}

// Child is the name of an object a sandbox creates for a workload or Service:
// "{base}-{sandbox}". Names that would be too long are cut and end in a short
// hash, so two long names never collide.
func Child(base, sandbox string) string {
	name := base + "-" + sandbox
	if len(name) <= MaxNameLength {
		return name
	}
	sum := sha256.Sum256([]byte(name))
	suffix := "-" + hex.EncodeToString(sum[:])[:8]
	return strings.TrimRight(name[:MaxNameLength-len(suffix)], "-") + suffix
}

// Labels are the labels on every object a sandbox creates for one fork.
func Labels(sandbox, fork string) map[string]string {
	return map[string]string{
		v1alpha1.LabelSandbox: sandbox,
		v1alpha1.LabelFork:    fork,
	}
}
