# syntax=docker/dockerfile:1.7

FROM node:20-alpine AS dashboard-builder
WORKDIR /src/dashboard

COPY dashboard/package.json dashboard/package-lock.json ./
RUN npm ci

COPY dashboard/ ./
RUN npm run build

FROM golang:1.22-alpine AS builder
ARG TARGETOS=linux
ARG TARGETARCH=amd64

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=dashboard-builder /src/dashboard/dist ./dashboard/dist

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -o /out/orchestrator ./cmd/orchestrator

FROM alpine:3.20

RUN adduser -D -g '' hivemind
WORKDIR /app

COPY --from=builder /out/orchestrator /app/orchestrator
COPY prompts /app/prompts

USER hivemind
EXPOSE 8080
ENTRYPOINT ["/app/orchestrator"]
