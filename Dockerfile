ARG BUILDPLATFORM=linux/amd64

FROM --platform=$BUILDPLATFORM golang:1.19.0-alpine as golang
WORKDIR /linkerd-gamma-build
COPY go.mod go.sum ./
RUN go mod download
ARG TARGETARCH
COPY controller controller
# COPY pkg pkg
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH  go build -o /out/gamma-controller -tags production -mod=readonly -ldflags "-s -w" ./controller

FROM scratch
COPY --from=golang /out/gamma-controller /gamma-controller
ENTRYPOINT ["/gamma-controller"]
