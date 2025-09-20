FROM golang:1.24.4-alpine AS builder

RUN apk add --no-cache bash curl

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY ./computenode .
RUN GOOS=linux GOARCH=amd64 go build -o computenode computenode.go && chmod +x computenode

# For debugging
FROM alpine:3.22
# For production
# FROM gcr.io/distroless/base-debian11
COPY --from=builder /app/computenode /computenode
ENTRYPOINT ["/computenode"]
