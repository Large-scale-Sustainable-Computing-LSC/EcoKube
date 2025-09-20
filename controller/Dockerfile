FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/ci-aware-controller main.go

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=build /out/ci-aware-controller /ci-aware-controller
USER 65532:65532
ENTRYPOINT ["/ci-aware-controller"]
