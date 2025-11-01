FROM golang:1.24-alpine AS build
WORKDIR /kubenergysched
COPY kubenergysched/go.mod ./
COPY kubenergysched/go.sum ./
COPY kespolicy /kespolicy
RUN go mod download
COPY kubenergysched/ .
WORKDIR /kubenergysched/controller
RUN mkdir -p /out && CGO_ENABLED=0 go build -o /out/ci-aware-controller .
RUN mkdir -p /tmp/var-log-ciw

FROM python:3.12-slim AS controller-debug
WORKDIR /
COPY --from=build /out/ci-aware-controller /ci-aware-controller
RUN groupadd -g 65532 ciw \
    && useradd -u 65532 -g ciw -M -r ciw \
    && mkdir -p /var/log/ciw \
    && chown -R ciw:ciw /var/log/ciw
USER 65532:65532
ENTRYPOINT ["/ci-aware-controller"]

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=build /out/ci-aware-controller /ci-aware-controller
COPY --chown=65532:65532 --from=build /tmp/var-log-ciw /var/log/ciw
USER 65532:65532
ENTRYPOINT ["/ci-aware-controller"]
