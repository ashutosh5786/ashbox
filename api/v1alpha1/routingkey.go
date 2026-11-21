package v1alpha1

import (
	"crypto/sha256"
	"encoding/base32"
	"strings"
)

// DerivedKeyLength is the length of a routing key derived from a keySource
// or a UID. 12 base32 characters are 60 bits: collisions are not a
// practical concern, and the controller still refuses duplicates.
const DerivedKeyLength = 12

var keyEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// DeriveRoutingKey turns a stable identity into a routing key: lower-case
// letters and digits, DerivedKeyLength long. The same input always gives
// the same key, in the controller and in every client.
func DeriveRoutingKey(source string) string {
	// The prefix versions the scheme: a new scheme gets a new prefix, so
	// it can never produce the same keys by accident.
	sum := sha256.Sum256([]byte("ashbox.dev/routing-key/v1\x00" + source))
	return strings.ToLower(keyEncoding.EncodeToString(sum[:]))[:DerivedKeyLength]
}

// ComputeRoutingKey returns the routing key the spec asks for:
// spec.routingKey, else a key derived from spec.keySource, else a key
// derived from the UID. It is empty for a sandbox that has neither and is
// not created yet (no UID).
func (sb *Sandbox) ComputeRoutingKey() string {
	switch {
	case sb.Spec.RoutingKey != "":
		return sb.Spec.RoutingKey
	case sb.Spec.KeySource != "":
		return DeriveRoutingKey(sb.Spec.KeySource)
	case sb.UID != "":
		return DeriveRoutingKey("uid:" + string(sb.UID))
	}
	return ""
}

// EffectiveRoutingKey is the key the sandbox routes on: the one in status
// once the controller has assigned it (it never changes after that),
// otherwise ComputeRoutingKey.
func (sb *Sandbox) EffectiveRoutingKey() string {
	if sb.Status.RoutingKey != "" {
		return sb.Status.RoutingKey
	}
	return sb.ComputeRoutingKey()
}
