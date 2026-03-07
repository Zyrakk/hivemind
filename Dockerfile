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

FROM node:22-alpine

ENV HOME=/home/hivemind

RUN apk add --no-cache \
        bash \
        ca-certificates \
        git \
        sqlite-libs \
        tmux \
        tree \
    && npm install -g @anthropic-ai/claude-code @openai/codex \
    && addgroup -g 10001 -S hivemind \
    && adduser -D -u 10001 -G hivemind -h /home/hivemind hivemind \
    && mkdir -p /app/repos /data/sessions/cache /home/hivemind/.claude /home/hivemind/.codex \
    && chown -R hivemind:hivemind /app /data /home/hivemind

WORKDIR /data

COPY --from=builder /out/orchestrator /usr/local/bin/orchestrator
COPY --chown=hivemind:hivemind AGENTS.md /app/AGENTS.md
COPY --chown=hivemind:hivemind prompts /app/prompts
COPY --chown=hivemind:hivemind agents /app/agents
COPY --chown=hivemind:hivemind templates /app/templates

USER hivemind

EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/orchestrator"]
