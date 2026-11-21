// Package status computes a sandbox's phase from the readiness of its forks.
//
// Readiness in status.forks[] is always real time. Phase changes are damped:
//   - a fork counts as down only after it has been not ready for
//     DegradedAfter without a break;
//   - a down fork counts as up again only after it has been ready for
//     RecoveredAfter (a crash-looping pod is often ready for a few seconds).
//
// Routing is never delayed: the mesh only sends traffic to ready pods.
package status

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1alpha1 "ashbox/api/v1alpha1"
)

// Settings are the damping periods.
type Settings struct {
	DegradedAfter  time.Duration
	RecoveredAfter time.Duration
}

// DefaultSettings are 5 minutes down and 1 minute back up.
var DefaultSettings = Settings{DegradedAfter: 5 * time.Minute, RecoveredAfter: time.Minute}

// Observation is the readiness of one fork right now.
type Observation struct {
	Name    string
	Ready   bool
	Reason  string
	Message string
}

// Result is the new status of the forks, the phase, and how long until the
// next damped transition (zero when none is pending).
type Result struct {
	Forks   []v1alpha1.ForkStatus
	Phase   v1alpha1.Phase
	Requeue time.Duration
}

// Evaluate computes the new fork statuses and phase.
// prev is the last stored status; invalid marks a spec that can never work
// (unknown baseline, bad patch), which is Failed at once.
func (s Settings) Evaluate(now time.Time, prev v1alpha1.SandboxStatus, obs []Observation, invalid bool) Result {
	old := map[string]v1alpha1.ForkStatus{}
	for _, f := range prev.Forks {
		old[f.Name] = f
	}

	res := Result{Forks: make([]v1alpha1.ForkStatus, 0, len(obs))}
	var down, waiting int
	stamp := metav1.NewTime(now)

	for _, o := range obs {
		f := v1alpha1.ForkStatus{Name: o.Name, Ready: o.Ready, Reason: o.Reason, Message: o.Message}
		p, seen := old[o.Name]

		switch {
		case o.Ready && (!seen || p.NotReadySince == nil):
			// Healthy, nothing to remember.

		case o.Ready:
			// Back up after an outage.
			f.NotReadySince = p.NotReadySince
			f.ReadySince = p.ReadySince
			if f.ReadySince == nil {
				f.ReadySince = &stamp
			}
			outage := f.ReadySince.Sub(f.NotReadySince.Time)
			upFor := now.Sub(f.ReadySince.Time)
			if upFor >= s.RecoveredAfter {
				f.NotReadySince, f.ReadySince = nil, nil
			} else {
				if outage >= s.DegradedAfter {
					down++
				}
				res.Requeue = minPositive(res.Requeue, s.RecoveredAfter-upFor)
			}

		default:
			// Not ready. A drop during recovery continues the same outage.
			f.NotReadySince = p.NotReadySince
			if !seen || f.NotReadySince == nil {
				f.NotReadySince = &stamp
			}
			outage := now.Sub(f.NotReadySince.Time)
			if outage >= s.DegradedAfter {
				down++
			} else {
				waiting++
				res.Requeue = minPositive(res.Requeue, s.DegradedAfter-outage)
			}
		}
		res.Forks = append(res.Forks, f)
	}

	switch {
	case invalid:
		res.Phase = v1alpha1.PhaseFailed
	case len(obs) > 0 && down == len(obs):
		res.Phase = v1alpha1.PhaseFailed
	case down > 0:
		res.Phase = v1alpha1.PhaseDegraded
	case waiting > 0 && (prev.Phase == "" || prev.Phase == v1alpha1.PhasePending):
		// New sandbox still coming up. A blip on a Ready sandbox stays Ready.
		res.Phase = v1alpha1.PhasePending
	default:
		res.Phase = v1alpha1.PhaseReady
	}
	return res
}

func minPositive(a, b time.Duration) time.Duration {
	if b <= 0 {
		return a
	}
	if a == 0 || b < a {
		return b
	}
	return a
}
