package status

import (
	"testing"
	"time"

	v1alpha1 "github.com/ashutosh5786/ashbox/api/v1alpha1"
)

var t0 = time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)

// step runs Evaluate and feeds the result back as the previous status.
type sim struct {
	t    *testing.T
	s    Settings
	prev v1alpha1.SandboxStatus
}

func (m *sim) at(d time.Duration, obs ...Observation) Result {
	r := m.s.Evaluate(t0.Add(d), m.prev, obs, false)
	m.prev = v1alpha1.SandboxStatus{Phase: r.Phase, Forks: r.Forks}
	return r
}

func up(n string) Observation { return Observation{Name: n, Ready: true} }
func down(n string) Observation {
	return Observation{Name: n, Ready: false, Reason: "CrashLoopBackOff"}
}

func (m *sim) want(r Result, p v1alpha1.Phase) {
	m.t.Helper()
	if r.Phase != p {
		m.t.Fatalf("phase = %s, want %s", r.Phase, p)
	}
}

func TestNewSandboxPendingThenReady(t *testing.T) {
	m := &sim{t: t, s: DefaultSettings}
	r := m.at(0, down("a"), down("b"))
	m.want(r, v1alpha1.PhasePending)
	if r.Requeue != 5*time.Minute {
		t.Fatalf("requeue = %v", r.Requeue)
	}
	m.want(m.at(time.Minute, up("a"), down("b")), v1alpha1.PhasePending)
	m.want(m.at(2*time.Minute, up("a"), up("b")), v1alpha1.PhaseReady)
}

func TestNewSandboxNeverReadyFails(t *testing.T) {
	m := &sim{t: t, s: DefaultSettings}
	m.at(0, down("a"))
	m.want(m.at(5*time.Minute, down("a")), v1alpha1.PhaseFailed)
}

func TestBlipDoesNotChangePhase(t *testing.T) {
	m := &sim{t: t, s: DefaultSettings}
	m.want(m.at(0, up("a")), v1alpha1.PhaseReady)
	r := m.at(time.Minute, down("a"))
	m.want(r, v1alpha1.PhaseReady)
	if r.Forks[0].Ready {
		t.Fatal("fork readiness must be real time")
	}
	m.want(m.at(3*time.Minute, up("a")), v1alpha1.PhaseReady)
	r = m.at(4*time.Minute, up("a"))
	if r.Forks[0].NotReadySince != nil || r.Forks[0].ReadySince != nil {
		t.Fatal("timestamps not cleared after recovery")
	}
}

func TestDegradedAndRecovery(t *testing.T) {
	m := &sim{t: t, s: DefaultSettings}
	m.at(0, up("a"), up("b"))
	m.at(time.Minute, up("a"), down("b"))
	m.want(m.at(6*time.Minute, up("a"), down("b")), v1alpha1.PhaseDegraded)

	// Back up, but only for 30s: still Degraded.
	m.want(m.at(7*time.Minute, up("a"), up("b")), v1alpha1.PhaseDegraded)
	r := m.at(7*time.Minute+30*time.Second, up("a"), up("b"))
	m.want(r, v1alpha1.PhaseDegraded)
	if r.Requeue != 30*time.Second {
		t.Fatalf("requeue = %v", r.Requeue)
	}
	m.want(m.at(8*time.Minute, up("a"), up("b")), v1alpha1.PhaseReady)
}

func TestDropDuringRecoveryIsOneOutage(t *testing.T) {
	m := &sim{t: t, s: DefaultSettings}
	m.at(0, up("a"))
	m.at(time.Minute, down("a"))
	m.at(3*time.Minute, up("a"))
	r := m.at(3*time.Minute+20*time.Second, down("a"))
	if !r.Forks[0].NotReadySince.Time.Equal(t0.Add(time.Minute)) || r.Forks[0].ReadySince != nil {
		t.Fatalf("fork = %+v", r.Forks[0])
	}
	m.want(m.at(6*time.Minute, down("a")), v1alpha1.PhaseFailed)
}

func TestInvalidFailsAtOnce(t *testing.T) {
	r := DefaultSettings.Evaluate(t0, v1alpha1.SandboxStatus{}, []Observation{up("a")}, true)
	if r.Phase != v1alpha1.PhaseFailed {
		t.Fatalf("phase = %s", r.Phase)
	}
}

func TestNoForksIsReady(t *testing.T) {
	r := DefaultSettings.Evaluate(t0, v1alpha1.SandboxStatus{}, nil, false)
	if r.Phase != v1alpha1.PhaseReady || r.Requeue != 0 {
		t.Fatalf("result = %+v", r)
	}
}
