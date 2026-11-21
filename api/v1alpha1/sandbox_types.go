package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Labels and annotations that ashbox puts on the objects it creates.
const (
	// LabelSandbox names the sandbox that owns an object.
	LabelSandbox = "ashbox.dev/sandbox"
	// LabelFork names the fork (or local entry) inside the sandbox.
	LabelFork = "ashbox.dev/fork"
	// AnnotationCreatedBy records who created the sandbox.
	AnnotationCreatedBy = "ashbox.dev/created-by"
)

// WorkloadKind is the kind of baseline workload a fork copies.
// +kubebuilder:validation:Enum=Deployment;Rollout
type WorkloadKind string

const (
	KindDeployment WorkloadKind = "Deployment"
	// KindRollout is an Argo Rollout. Its fork is always a Deployment.
	KindRollout WorkloadKind = "Rollout"
)

// WorkloadRef points at a baseline workload.
type WorkloadRef struct {
	// +kubebuilder:default=Deployment
	Kind      WorkloadKind `json:"kind,omitempty"`
	Namespace string       `json:"namespace"`
	Name      string       `json:"name"`
}

// ImageOverride sets the image of one container.
type ImageOverride struct {
	Container string `json:"container"`
	Image     string `json:"image"`
}

// PatchType is the kind of patch applied to the forked workload.
// +kubebuilder:validation:Enum=strategic;merge;json
type PatchType string

// Patch is applied to the forked Deployment after images and env.
type Patch struct {
	Type  PatchType `json:"type"`
	Value string    `json:"value"`
}

// Customizations change the fork compared to its baseline.
type Customizations struct {
	Images []ImageOverride `json:"images,omitempty"`
	Env    []corev1.EnvVar `json:"env,omitempty"`
	Patch  *Patch          `json:"patch,omitempty"`
}

// Fork runs a changed copy of a baseline workload in the cluster.
type Fork struct {
	ForkOf         WorkloadRef     `json:"forkOf"`
	Customizations *Customizations `json:"customizations,omitempty"`
}

// ServiceRef points at a Service.
type ServiceRef struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// LocalMapping sends one baseline Service port to one port of the tunnel Service.
type LocalMapping struct {
	// Port is the baseline Service port.
	Port int32 `json:"port"`
	// TunnelPort is the port of the tunnel Service. Defaults to Port.
	TunnelPort int32 `json:"tunnelPort,omitempty"`
}

// Local sends keyed traffic for a baseline workload to a laptop.
//
// The laptop side is k8s-tunnel: `ashctl` exposes a local port, and the tunnel
// server publishes it as an ordinary Service. ashbox only routes keyed
// requests for the baseline Services to that tunnel Service.
type Local struct {
	Name     string         `json:"name"`
	From     WorkloadRef    `json:"from"`
	Tunnel   ServiceRef     `json:"tunnel"`
	Mappings []LocalMapping `json:"mappings"`
}

// TTLOffset is the time a TTL counts from.
// +kubebuilder:validation:Enum=createdAt;updatedAt
type TTLOffset string

const (
	TTLFromCreated TTLOffset = "createdAt"
	TTLFromUpdated TTLOffset = "updatedAt"
)

// TTL is how long a sandbox lives.
type TTL struct {
	// Duration such as "4h", "30m" or "14d".
	Duration string `json:"duration"`
	// +kubebuilder:default=createdAt
	OffsetFrom TTLOffset `json:"offsetFrom,omitempty"`
}

// SandboxSpec is the wanted state of a sandbox.
//
// The routing key never changes after creation: routingKey and keySource
// cannot be added, removed or changed by an update.
//
// +kubebuilder:validation:XValidation:rule="has(self.routingKey) == has(oldSelf.routingKey)",message="routingKey cannot be added or removed after creation"
// +kubebuilder:validation:XValidation:rule="has(self.keySource) == has(oldSelf.keySource)",message="keySource cannot be added or removed after creation"
// +kubebuilder:validation:XValidation:rule="!(has(self.routingKey) && has(self.keySource))",message="set routingKey or keySource, not both"
type SandboxSpec struct {
	// RoutingKey sets the routing key directly: 8 to 32 lower-case letters,
	// digits and '-'. Immutable.
	// +optional
	// +kubebuilder:validation:Pattern=`^[a-z0-9][a-z0-9-]{6,30}[a-z0-9]$`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="routingKey is immutable"
	RoutingKey string `json:"routingKey,omitempty"`

	// KeySource is a stable identity the routing key is derived from, such
	// as "github.com/acme/shop#42". The same source always gives the same
	// key, also when the sandbox is deleted and created again. Immutable.
	//
	// With neither routingKey nor keySource, the key is derived from the
	// sandbox's UID: unique, but new for every sandbox.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="keySource is immutable"
	KeySource string `json:"keySource,omitempty"`

	Description string  `json:"description,omitempty"`
	TTL         *TTL    `json:"ttl,omitempty"`
	Forks       []Fork  `json:"forks,omitempty"`
	Local       []Local `json:"local,omitempty"`
}

// Phase is the reported state of a sandbox.
// +kubebuilder:validation:Enum=Pending;Ready;Degraded;Failed;Expired
type Phase string

const (
	PhasePending  Phase = "Pending"
	PhaseReady    Phase = "Ready"
	PhaseDegraded Phase = "Degraded"
	PhaseFailed   Phase = "Failed"
	PhaseExpired  Phase = "Expired"
)

// ForkStatus is the state of one fork.
type ForkStatus struct {
	Name  string `json:"name"`
	Ready bool   `json:"ready"`
	// NotReadySince is when the fork went down. Cleared once it has recovered.
	NotReadySince *metav1.Time `json:"notReadySince,omitempty"`
	// ReadySince is when a down fork came back. Set only while recovering.
	ReadySince *metav1.Time `json:"readySince,omitempty"`
	Reason     string       `json:"reason,omitempty"`
	Message    string       `json:"message,omitempty"`
}

// Condition types.
const (
	// ConditionRoutesProgrammed is False with reason NoWaypoint when a
	// namespace has no waypoint, so the routes exist but nothing enforces them.
	ConditionRoutesProgrammed = "RoutesProgrammed"
)

// SandboxStatus is the observed state of a sandbox.
type SandboxStatus struct {
	Phase Phase `json:"phase,omitempty"`
	// RoutingKey is the key requests carry to reach this sandbox:
	// "baggage: <baggage key>=<routingKey>". Set once by the controller,
	// then never changed.
	RoutingKey string             `json:"routingKey,omitempty"`
	ExpiresAt  *metav1.Time       `json:"expiresAt,omitempty"`
	Forks      []ForkStatus       `json:"forks,omitempty"`
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// Sandbox is a set of forked workloads (and laptop tunnels) reached by one
// routing key.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// Cluster-scoped, because a sandbox owns objects in many namespaces and an
// ownerReference may not cross namespaces to a namespaced owner.
//
// +kubebuilder:resource:scope=Cluster,shortName=sbx
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Key",type=string,JSONPath=`.status.routingKey`
// +kubebuilder:printcolumn:name="Expires",type=string,JSONPath=`.status.expiresAt`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type Sandbox struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SandboxSpec   `json:"spec,omitempty"`
	Status SandboxStatus `json:"status,omitempty"`
}

// SandboxList is a list of sandboxes.
//
// +kubebuilder:object:root=true
type SandboxList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Sandbox `json:"items"`
}
