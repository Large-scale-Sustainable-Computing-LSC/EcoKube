FROM golang:1.24.4-alpine AS builder

RUN apk add --no-cache bash curl

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . ./
RUN GOOS=linux GOARCH=amd64 go build -o powertrace-bin ./powertrace && chmod +x powertrace-bin

# For debugging
FROM alpine:3.22

RUN apk add --no-cache curl

# For production
# FROM gcr.io/distroless/base-debian11
COPY --from=builder /app/powertrace-bin /powertrace-bin
ENTRYPOINT ["/powertrace-bin"]

