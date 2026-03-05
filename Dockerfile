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

FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        git \
        nodejs \
        npm \
        tmux \
    && npm install -g @openai/codex \
    && npm cache clean --force \
    && rm -rf /var/lib/apt/lists/* /root/.npm \
    && mkdir -p /root/.codex

WORKDIR /app

COPY --from=builder /out/orchestrator /app/orchestrator
COPY prompts /app/prompts
COPY agents /app/agents
COPY templates /app/templates
RUN mkdir -p /app/sessions/cache

EXPOSE 8080
ENTRYPOINT ["/app/orchestrator"]
