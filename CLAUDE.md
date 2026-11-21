# ashbox project notes

## Goal

Sandboxes for Kubernetes services: forked workloads reached by a routing key in the `baggage` header, through Gateway API routes enforced by the mesh. TTL, status phases, cleanup by ownerReferences.

k8s-tunnel (`D:\k8s-tunnel`, module `k8s-tunnel`) is an extension of ashbox. It adds local development: `ashctl` exposes a laptop port as a Service, and a sandbox `local` entry routes keyed traffic to it. `ashctl` is the user-facing CLI for both.

## Rules

- This is the owner's personal project. No company names in code, API groups, labels or docs. The API group is `ashbox.dev`.
- Hosted on GitHub; module path `github.com/ashutosh5786/ashbox`, so `ashctl` (k8s-tunnel) can import `api/v1alpha1`.
- Runnable anywhere: any cluster reached by kubeconfig, no cloud SDK; any GAMMA mesh (plain Gateway API objects); mesh-specific checks only behind `--mesh`; optional dependencies (Argo Rollouts) read as unstructured; pure Go, `CGO_ENABLED=0`, linux/amd64 and linux/arm64.
- Every package with code has tests. Keep rendering pure and test it without a cluster; fake the Kubernetes API with controller-runtime's fake client (envtest later).
- After changing `api/`, run `make generate`. Do not edit `zz_generated.deepcopy.go` or the CRD by hand.
- Keep the chart RBAC in step with the `+kubebuilder:rbac` markers.
- The owner pushes and runs anything that changes a cluster. Commit only when asked.

## Settled decisions

- `Sandbox` is cluster-scoped, because children live in many namespaces and ownerReferences cannot cross namespaces to a namespaced owner.
- One HTTPRoute per baseline Service port per sandbox; never edit baseline objects.
- Routing key = sandbox name, lower case. Baggage entry name is a flag (default `routing-key`).
- Failed forks keep their route (503), never silent fallback to baseline.
- Damping: down after 5m not ready, back up after 1m ready (flags).
- Fork pods drop the label keys used by baseline Service selectors.

## Open points

- `offsetFrom: updatedAt` needs the time of the last spec change; not tracked yet.
- Rollouts with `spec.workloadRef` are not supported yet.

## Not built yet

1. Metrics (sandboxes by phase, forks ready, time to ready).
2. `ashctl sandbox create/get/list/delete` and `ashctl local` in k8s-tunnel.
3. envtest suite.
4. Status reason `UpdateFailed` when a new image of a working fork does not roll out.
5. Routes only for HTTP-like ports (today every Service port gets a route).

## Environment notes

- Local test cluster: Docker Desktop Kubernetes (kind, 2 nodes, context `docker-desktop`) with Istio ambient and Gateway API v1.7.0. Demo in `deploy/demo`.
- The agent guard blocks all cluster writes, including `kubectl exec`, `scale` and running the controller against a cluster. The owner runs those; the agent uses `kubectl get/describe/logs`.
- On this Windows machine `bash` is the WSL launcher; use `"C:\Program Files\Git\bin\bash.exe"`.
