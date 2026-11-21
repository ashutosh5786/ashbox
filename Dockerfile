# Multi-architecture build: docker buildx build --platform linux/amd64,linux/arm64 .
FROM --platform=$BUILDPLATFORM golang:1.27 AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY api ./api
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/ashbox-controller ./cmd/ashbox-controller

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/ashbox-controller /ashbox-controller
USER 65532:65532
ENTRYPOINT ["/ashbox-controller"]
