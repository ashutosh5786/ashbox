BIN      := bin
IMAGE    ?= ashbox-controller:dev
PLATFORMS ?= linux/amd64,linux/arm64

.PHONY: all generate test vet build cross image lint-chart clean

all: generate vet test build

## generate: deep-copy code and the CRD, from the types in api/
generate:
	go tool controller-gen object crd output:crd:dir=./deploy/chart/ashbox/crds paths=./api/v1alpha1

vet:
	go vet ./...

test:
	go test ./...

build:
	CGO_ENABLED=0 go build -trimpath -o $(BIN)/ashbox-controller ./cmd/ashbox-controller

## cross: Linux amd64 and arm64 binaries
cross:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o $(BIN)/ashbox-controller-linux-amd64 ./cmd/ashbox-controller
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o $(BIN)/ashbox-controller-linux-arm64 ./cmd/ashbox-controller

image:
	docker buildx build --platform $(PLATFORMS) -t $(IMAGE) .

lint-chart:
	helm lint deploy/chart/ashbox
	helm template ashbox deploy/chart/ashbox --namespace ashbox-system > /dev/null

clean:
	rm -rf $(BIN)
