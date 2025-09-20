FROM golang:1.24-alpine AS builder

WORKDIR /scheduler
COPY go.mod go.sum ./
RUN go mod download

COPY . ./
# RUN go get benchmark.go
RUN GOOS=linux GOARCH=amd64 go build -o benchmark_bin ./benchmark/benchmark.go && chmod +x benchmark_bin

# For debugging
FROM alpine:3.22
# For production
# FROM gcr.io/distroless/base-debian11
COPY --from=builder /scheduler/benchmark_bin /benchmark
ENTRYPOINT ["/benchmark"]

