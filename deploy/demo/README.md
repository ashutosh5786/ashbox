# Demo: header routing on a local cluster

This demo installs Istio ambient on a local cluster and shows the routing that ashbox will create, written by hand. It is the same test as the design spike.

Tested target: Docker Desktop Kubernetes (kind, 2 nodes). Any cluster works.

## 1. Gateway API CRDs

```bash
kubectl apply --server-side -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.7.0/standard-install.yaml
```

## 2. Istio ambient

```bash
helm repo add istio https://istio-release.storage.googleapis.com/charts
helm repo update
helm install istio-base istio/base -n istio-system --create-namespace --wait
helm install istiod istio/istiod -n istio-system --set profile=ambient --wait
helm install istio-cni istio/cni -n istio-system --set profile=ambient --wait
helm install ztunnel istio/ztunnel -n istio-system --wait
kubectl get pods -n istio-system
```

All pods must be Running: `istiod`, one `istio-cni-node` and one `ztunnel` on each node.

## 3. Demo app and the hand-written fork

Run from the repository root:

```bash
kubectl apply -f deploy/demo/
kubectl wait -n demo --for=condition=Programmed gateway/waypoint --timeout=120s
kubectl wait -n demo --for=condition=Ready pod --all --timeout=120s
kubectl get httproute -n demo
```

## 4. Test

```bash
bash deploy/demo/test.sh
```

Or by hand:

```bash
# No key: baseline
kubectl exec -n demo client -- curl -s echo:8080
# Key demo1: the fork
kubectl exec -n demo client -- curl -s -H "baggage: routing-key=demo1" echo:8080
# Key among other entries, with properties: the fork
kubectl exec -n demo client -- curl -s -H "baggage: foo=bar, routing-key=demo1;p=1" echo:8080
# Near-miss keys: baseline
kubectl exec -n demo client -- curl -s -H "baggage: routing-key=demo" echo:8080
kubectl exec -n demo client -- curl -s -H "baggage: routing-key=demo12" echo:8080
# Fork scaled to 0: 503, not the baseline
kubectl scale -n demo deploy/echo-demo1 --replicas=0
kubectl exec -n demo client -- curl -s -o /dev/null -w "%{http_code}\n" -H "baggage: routing-key=demo1" echo:8080
kubectl scale -n demo deploy/echo-demo1 --replicas=1
```

Expected: `baseline`, `fork demo1`, `fork demo1`, `baseline`, `baseline`, `503`.

## 5. The controller creates a sandbox

Install the CRD, then run the controller from your machine (it uses your kubeconfig):

```bash
kubectl apply -f deploy/chart/ashbox/crds/
go run ./cmd/ashbox-controller --mesh=istio --metrics-bind-address=0
```

In a second terminal:

```bash
kubectl apply -f deploy/demo/sandbox/demo2.yaml
kubectl get sandbox demo2 -w          # Pending, then Ready
bash deploy/demo/test.sh demo2
kubectl delete sandbox demo2          # all its children go too
```

## Clean up

```bash
kubectl delete -f deploy/demo/
```
