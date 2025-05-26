FROM docker.repoifb.local/golang:1.24.3 AS builder

WORKDIR /app

COPY vendor ./vendor
ENV GOFLAGS="-mod=vendor"
ENV GOPROXY=direct

COPY go.mod go.sum ./
# RUN go mod download

COPY main.go ./
RUN CGO_ENABLED=0 go build -o conship main.go

FROM docker.repoifb.local/debian:buster-slim

COPY --from=builder /app/conship /usr/local/bin/conship
ENTRYPOINT ["conship"]
