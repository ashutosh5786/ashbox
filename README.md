# ashbox

Sandboxes for services on Kubernetes. A sandbox runs changed copies ("forks") of some services next to the normal ones. Requests that carry the sandbox's routing key go to the forks. All other requests go to the normal services, which stay unchanged.

```
caller ──► Service cart ──► mesh reads "baggage: routing-key=pr-42"
                              ├── key pr-42  ──► cart-pr-42 (fork)
                              └── no key     ──► cart       (baseline)
```

With [k8s-tunnel](#local-development-with-k8s-tunnel), a sandbox can also send keyed requests to a service that runs on your laptop.

## Status

Early. These parts work and have tests:

- The `Sandbox` API (`ashbox.dev/v1alpha1`) and its CRD.
- The controller creates, updates and removes forks (Deployment or Rollout baseline), fork Services and HTTPRoutes. It routes `local` entries to k8s-tunnel Services, with a `ReferenceGrant`.
- Status: phases with damping, per-fork reason (for example `CrashLoopBackOff`), conditions `SpecValid` and `RoutesProgrammed` (`--mesh=istio`).
- TTL: expiry date, and deletion when it runs out.
- The controller never takes over an object it did not create.

The header routing was checked by hand on Istio ambient (`deploy/demo`).

The `ashctl` CLI in [k8s-tunnel](https://github.com/ashutosh5786/k8s-tunnel) creates and manages sandboxes.

Not built yet: metrics, published images and binaries, and tests against a real API server (envtest).

## Getting started

There are two parts. The cluster side is installed once per cluster, by an administrator. The CLI side is installed by each developer.

| Part | Repository | Who installs it |
|--|--|--|
| ashbox controller | this repository | cluster administrator |
| k8s-tunnel server (only for laptop development) | [k8s-tunnel](https://github.com/ashutosh5786/k8s-tunnel) | cluster administrator |
| `ashctl` CLI | [k8s-tunnel](https://github.com/ashutosh5786/k8s-tunnel) | each developer |

You need `kubectl`, `helm`, Go 1.27 or later, Docker with `buildx`, and a container registry that the cluster can pull from.

### Cluster side (once per cluster)

**1. Gateway API CRDs.** Skip this step if the cluster has them.

```bash
kubectl apply --server-side -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.7.0/standard-install.yaml
```

**2. A mesh that supports GAMMA.** For example, Istio ambient:

```bash
helm repo add istio https://istio-release.storage.googleapis.com/charts && helm repo update
helm install istio-base istio/base -n istio-system --create-namespace --wait
helm install istiod istio/istiod -n istio-system --set profile=ambient --wait
helm install istio-cni istio/cni -n istio-system --set profile=ambient --wait
helm install ztunnel istio/ztunnel -n istio-system --wait
```

**3. Put each namespace with services to fork in the mesh.** With Istio ambient, this means a label and a waypoint. Change `shop` to your namespace:

```bash
kubectl label namespace shop istio.io/dataplane-mode=ambient istio.io/use-waypoint=waypoint
kubectl apply -f - <<'EOF'
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: waypoint
  namespace: shop
  labels: { istio.io/waypoint-for: all }
spec:
  gatewayClassName: istio-waypoint
  listeners: [{ name: mesh, port: 15008, protocol: HBONE }]
EOF
```

**4. Add a catch-all route to each Service that can be forked.** Requests without a key then go to the normal pods. See `deploy/demo/20-baseline.yaml` for an example.

**5. Install the ashbox controller.** The chart uses the published image `docker.io/ashutosh5786/ashbox-controller` (amd64 and arm64):

```bash
git clone https://github.com/ashutosh5786/ashbox && cd ashbox
helm install ashbox deploy/chart/ashbox -n ashbox-system --create-namespace \
  --set controller.mesh=istio \
  --set 'users.subjects[0].kind=Group,users.subjects[0].name=developers,users.subjects[0].apiGroup=rbac.authorization.k8s.io'
```

`users.subjects` gives a group the right to create sandboxes. Use your own group name.

To build the image yourself: `docker buildx build --platform linux/amd64,linux/arm64 -t REGISTRY/ashbox-controller:0.1.0 --push .`, then add `--set image.repository=REGISTRY/ashbox-controller` to `helm install`.

**6. For laptop development, install the k8s-tunnel server.** See "Deploy the server" in the [k8s-tunnel README](https://github.com/ashutosh5786/k8s-tunnel#deploy-the-server).

**Check:** `kubectl get pods -n ashbox-system` shows the controller Running, and `kubectl get crd sandboxes.ashbox.dev` exists.

### Developer side

**1. Install `ashctl`:**

```bash
git clone https://github.com/ashutosh5786/k8s-tunnel && cd k8s-tunnel
go build -o bin/ ./cmd/ashctl
# Put bin/ashctl (bin\ashctl.exe on Windows) in a folder on your PATH.
```

**2. Fork a service with a new image:**

```bash
ashctl sandbox create pr-42 --fork shop/cart=app:REGISTRY/cart:pr-42 --wait
curl -H "baggage: routing-key=pr-42" http://cart.shop:8080/    # reaches the fork
ashctl sandbox get pr-42
ashctl sandbox delete pr-42
```

**3. Or run the service on your laptop:**

```bash
ashctl connect
ashctl local shop/cart --port 8080:3000     # your localhost:3000
# Requests with "baggage: routing-key=<your user name>" for cart now reach your laptop.
ashctl disconnect                           # also deletes the sandbox
```

Your services must forward the `baggage` header on their outgoing calls (OpenTelemetry does this). Then the key also reaches forks deeper in the call chain.

## Runs anywhere

- **Any cluster** that you reach with a kubeconfig: EKS, AKS, GKE, kind, k3s. ashbox uses no cloud SDK.
- **Any mesh** that supports the Gateway API for service meshes (GAMMA): HTTPRoutes attached to a Service. Examples are Istio (ambient with a waypoint, or sidecars), Linkerd and Cilium. ashbox writes plain Gateway API objects. Checks for one mesh only are behind `--mesh` (now `none` or `istio`).
- **Any CPU:** pure Go (`CGO_ENABLED=0`). The image is built for `linux/amd64` and `linux/arm64`.
- **Argo Rollouts are optional.** ashbox reads a Rollout as an unstructured object and copies it into a Deployment.

### Requirements

1. The Gateway API CRDs (`HTTPRoute`, `ReferenceGrant`).
2. A mesh that applies HTTPRoutes attached to Services (GAMMA).
3. A catch-all route for each baseline Service. Requests without a key then go to the baseline. Some meshes do this without a route.
4. Services that forward the `baggage` header on their outgoing calls. OpenTelemetry does this.

## How it works

The controller does these steps for each fork in a sandbox:

1. It copies the baseline Deployment (or Rollout) into `{name}-{sandbox}`, with one replica, the new images, env and patch.
2. It removes the labels that the baseline Services select on from the fork's pods. The baseline Services then never send traffic to the fork.
3. It copies each baseline Service into `{service}-{sandbox}`, which selects only the fork's pods.
4. It attaches an HTTPRoute to each baseline Service port. The route matches the header `baggage` with the regex `(^|.*,)\s*routing-key=pr-42\s*([,;].*)?$` and sends the request to the fork Service. The regex is anchored on both sides, so `pr-4` never matches `pr-42`.

Each sandbox owns its own objects through ownerReferences. Deleting a sandbox deletes all of them. Sandboxes never edit shared objects, so many sandboxes can fork the same service at the same time. `Sandbox` is cluster-scoped, because an ownerReference cannot point across namespaces to a namespaced owner.

### Status phases

| Phase | Meaning |
|--|--|
| Pending | New sandbox, and not all forks are ready yet (in the first 5 minutes). |
| Ready | All forks are ready. Breaks shorter than 5 minutes do not change this. |
| Degraded | Some forks have not been ready for more than 5 minutes. |
| Failed | No fork has been ready for 5 minutes, or the spec is invalid. |
| Expired | The TTL ran out. The sandbox is deleted. |

A down fork counts as ready again after it is ready for 1 minute. A failed fork keeps its route, so keyed requests to it get a 503. They do not go to the baseline without a warning. Change the periods with `--degraded-after` and `--recovered-after`.

## Local development with k8s-tunnel

k8s-tunnel is an extension of ashbox. It connects a laptop to the cluster:

1. `ashctl connect --expose checkout-ash:8080:3000/http` publishes the laptop's port 3000 as Service `checkout-ash` in the `k8s-tunnel` namespace.
2. A `local` entry in the sandbox points the baseline `checkout` at that Service. ashbox adds an HTTPRoute and a `ReferenceGrant`, which allows the route to reach the other namespace.
3. Keyed requests for `checkout` go to the laptop. Outbound calls from the laptop reach the cluster through `ashctl --hosts`.

See `deploy/examples/sandbox.yaml`.

## Build and test

```bash
make generate   # deep-copy code and CRD, after a change in api/
make test
make build      # bin/ashbox-controller
make cross      # Linux amd64 and arm64
make image      # multi-architecture image
```

## Install

```bash
helm install ashbox deploy/chart/ashbox \
  --namespace ashbox-system --create-namespace \
  --set image.repository=<your-registry>/ashbox-controller
kubectl apply -f deploy/examples/sandbox.yaml
kubectl get sandboxes
```

| Value | Default | Meaning |
|--|--|--|
| `controller.baggageKey` | `routing-key` | Baggage entry that holds the routing key. |
| `controller.mesh` | `none` | `istio` also reports namespaces without a waypoint. |
| `controller.degradedAfter` | `5m` | Time not ready before a fork counts as down. |
| `controller.recoveredAfter` | `1m` | Time ready before a down fork counts as up. |
| `users.subjects` | `[]` | Who may create sandboxes (binds ClusterRole `<release>-user`). |

## Layout

| Path | Contents |
|--|--|
| `api/v1alpha1` | The `Sandbox` type. `zz_generated.deepcopy.go` is generated. |
| `cmd/ashbox-controller` | The controller command. |
| `internal/controller` | The reconcile loop. |
| `internal/fork` | Fork Deployment and Service rendering. |
| `internal/route` | HTTPRoute, ReferenceGrant and the key regex. |
| `internal/status` | Phase computation with damping. |
| `internal/ttl` | TTL parsing (`4h`, `14d`). |
| `internal/names` | Child names, labels and routing key. |
| `deploy/chart/ashbox` | Helm chart. The CRD in `crds/` is generated. |
