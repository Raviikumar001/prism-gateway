# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS builder
WORKDIR /src
RUN apk add --no-cache ca-certificates git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /prism ./cmd/prism

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=builder /prism /prism
COPY data /data
ENV PORT=8080
ENV DATA_DIR=/data
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/prism"]
