FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY go.sum ./
RUN go mod download
COPY . .
WORKDIR /src/controller
RUN CGO_ENABLED=0 go build -o /out/ci-aware-controller .

FROM python:3.12-slim AS controller-debug
WORKDIR /
COPY --from=build /out/ci-aware-controller /ci-aware-controller
RUN groupadd -g 65532 ciw && useradd -u 65532 -g ciw -M -r ciw
USER 65532:65532
ENTRYPOINT ["/ci-aware-controller"]

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=build /out/ci-aware-controller /ci-aware-controller
USER 65532:65532
ENTRYPOINT ["/ci-aware-controller"]
