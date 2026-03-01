# Flux
## Ultima actualizacion: 2026-02-27

## 1. Objetivo
Flux es una plataforma self-hosted de briefing inteligente que ingesta noticias tecnicas desde RSS, Hacker News, Reddit y GitHub Releases, las filtra por relevancia con embeddings y genera un briefing diario con LLM. Esta orientado a equipos o personas que necesitan vigilancia informativa diaria sin revisar cientos de fuentes manualmente. Existe para reducir tiempo de lectura, priorizar senal sobre ruido y permitir feedback continuo por seccion.

## 2. Stack Tecnologico
- Lenguaje: Go 1.23.0 (backend/workers), TypeScript 5 + Svelte 5 (frontend), Python 3.11 (embeddings-svc)
- Framework: API en chi v5.1.0; frontend en SvelteKit 2.52.0 (adapter-node); embeddings en FastAPI 0.115.6
- Base de datos: PostgreSQL 16 + pgvector (vector(384))
- Dependencias clave: github.com/jackc/pgx/v5 v5.7.2, github.com/nats-io/nats.go v1.38.0, github.com/redis/go-redis/v9 v9.7.0, github.com/mmcdole/gofeed v1.3.0, github.com/go-shiori/go-readability v0.0.0-20251205110129-5db1dc9836f0, github.com/pgvector/pgvector-go v0.2.2, github.com/sirupsen/logrus v1.9.3, sentence-transformers 3.3.1, torch 2.10.0+cpu
- CI/CD: GitHub Actions (`lint`, `test`, `build+push` multi-arch a GHCR)
- Contenedorizacion: Docker multi-stage por servicio + Docker Compose + Helm chart
- Plataforma de despliegue: k3s con Traefik/cert-manager (objetivo principal) y Docker Compose para entorno local/self-hosted

## 3. Convenciones de Codigo
- Formato: gofmt estandar en Go; svelte-check + TypeScript strict en frontend
- Naming: Go idiomatico (PascalCase exports, camelCase internos); componentes Svelte en PascalCase; rutas SvelteKit por filesystem routing
- Estructura de directorios: `cmd/` binaries por servicio, `internal/` logica de dominio, `migrations/` SQL versionado, `deploy/docker/` Dockerfiles, `deploy/helm/flux/` chart, `web/` frontend SvelteKit
- Patron de commits: historial mixto, pero para este repo se debe usar Conventional Commits con scope de servicio (`feat(worker-rss): ...`, `fix(processor): ...`, `feat(web): ...`, `ci: ...`, `docs: ...`). Evitar mensajes genericos (`.`, `Rebuild frontend and style`).
- Manejo de errores: retorno de `error` con contexto (`fmt.Errorf("...: %w")`), sin `panic` en flujo de negocio; `Fatal` solo en arranque de procesos
- Logging: logrus JSON en backend/workers; campos estructurados en eventos relevantes
- Tests: tests en `*_test.go` junto al paquete; uso de `testify` y enfoque table-driven donde aplica

## 4. Arquitectura
### 4.1 Descripcion de alto nivel
Workers de ingesta publican eventos `articles.new` en NATS JetStream. El `processor` calcula embeddings, aplica deduplicacion semantica, asigna seccion y score de relevancia, y actualiza estado en PostgreSQL/pgvector. `briefing-gen` clasifica y resume con LLM por seccion y guarda el briefing final. La API Go expone endpoints REST para articulos, fuentes, secciones, briefings y feedback; el frontend SvelteKit consume esos endpoints via proxy `/api/*` y soporta auth por token y modo PWA. Si el `processor` se queda atras, los workers siguen ingestando y los articulos quedan `pending`; `briefing-gen` solo toma `pending` con `relevance_score` ya calculado, por lo que los no procesados no salen en briefing.

### 4.2 Componentes
- `worker-rss`: ingesta feeds RSS/Atom, normaliza URLs, deduplica y publica eventos
- `worker-hn`: ingesta Hacker News (`top/best/new`), filtra por score y publica eventos
- `worker-reddit`: ingesta subreddits via OAuth script flow y publica eventos
- `worker-github`: ingesta releases de repos GitHub y publica eventos
- `processor`: embeddings, asignacion de seccion, score de relevancia, ajuste dinamico de threshold y dedup semantica
- `briefing-gen`: clasificacion + resumen + sintetesis de briefing diario por secciones
- `api`: expone REST, auth bearer opcional, validacion RSS y feedback loop
- `profile/recalculator`: recalcula `section_profiles` (inmediato u horario) con EMA
- `store`: capa de acceso a PostgreSQL, consultas API y migraciones
- `queue`: wrapper NATS JetStream para publish/subscribe durable
- `ratelimit`: rate limiter Redis-backed + backoff/jitter + transporte HTTP
- `embeddings-svc`: FastAPI local con all-MiniLM-L6-v2 (384 dims)
- `web`: UI SvelteKit para briefing, feed, admin y login
- `NATS stream ARTICLES`: `WorkQueuePolicy` + `MaxAge 72h`; si el consumer cae mas de 72h, eventos pueden expirar
- `NATS stream BRIEFING`: `WorkQueuePolicy` + `MaxAge 24h`
- `processor consumer`: durable `flux-processor`; en error hace `NAK` y depende de redelivery de JetStream

### 4.3 Decisiones de arquitectura
- Decision 1: Decision: pipeline asincrono orientado a eventos con NATS JetStream (`articles.new`). Razon: desacopla workers, processor y briefing-gen, y mejora resiliencia ante fallos parciales. Alternativa descartada: llamadas sincronas directas entre servicios.
- Decision 2: Decision: PostgreSQL + pgvector como fuente unica de verdad para datos relacionales y embeddings. Razon: simplifica operacion y evita coordinar multiples stores. Alternativa descartada: separar base relacional y motor vectorial independiente desde el inicio.
- Decision 3: Decision: abstraccion LLM via interfaz `internal/llm.Analyzer` con proveedores `glm`, `openai_compat`, `anthropic`. Razon: reduce lock-in y permite cambiar proveedor por configuracion. Alternativa descartada: acoplar toda la logica a un solo proveedor.
- Decision 4: Decision: control centralizado de trafico HTTP externo en `internal/ratelimit` + Redis. Razon: evita baneos y estandariza jitter/backoff/retry-after para todos los workers. Alternativa descartada: limites ad-hoc por worker sin coordinacion global.
- Decision 5: Decision: mantener workers de ingesta desacoplados del `processor` (sin backpressure duro). Razon: prioriza captura de fuentes aun con degradacion de procesamiento. Alternativa descartada: frenar ingesta cuando processor se retrasa.
- Decision 6: Decision: `embeddings-svc` local en una replica y con `model_lock` global. Razon: simplicidad operativa y menor consumo en homelab. Alternativa descartada: servicio de embeddings horizontal y concurrente desde dia 1.

### 4.4 Diagrama (opcional)
`Workers (RSS/HN/Reddit/GitHub) -> NATS (articles.new) -> Processor (embeddings + relevance + semantic dedup) -> PostgreSQL/pgvector -> Briefing-Gen (LLM) -> Briefings -> API (chi) -> Frontend (SvelteKit /api proxy)`

## 5. Estado Actual
### Completado
- Ingesta multi-fuente implementada: RSS, Hacker News, Reddit y GitHub Releases
- Pipeline de procesamiento implementado: embeddings locales, score de relevancia por seccion, thresholds dinamicos y dedup semantica
- API REST implementada para articulos, fuentes, secciones, briefings y feedback
- Feedback loop implementado con recalc de `section_profiles` inmediato u horario
- Frontend funcional implementado (`/`, `/feed`, `/admin/sources`, `/login`) con acciones de feedback
- Auth bearer opcional + modo reverse-proxy + PWA/service worker
- Despliegue y operacion implementados en Docker Compose y Helm (k3s)
- CI en GitHub Actions implementada con lint, test y push de imagenes multi-arch

### En Curso
- no definido en el repositorio (sin metadata explicita de trabajo activo en `main`)
- Falta de observabilidad operativa del pipeline: no hay metricas Prometheus para backlog NATS, latencia processor ni throughput de embeddings; hoy se depende de logs + consultas SQL.

### Pendiente
- P0: job de reconciliacion/replay para articulos `pending` sin evento activo (riesgo tras expiracion de mensajes NATS a 72h)
- P0: metricas y alertas de backpressure (`pending` sin embedding, `pending` con score, errores de redelivery)
- P1: busqueda semantica y endpoints conversacionales (`/api/search`, `/api/ask`)
- P1: story threading / seguimiento de temas (modelo de stories + UI)
- P2: threat intelligence (ingesta NVD/CVE + match contra inventario de infraestructura)
- P2: notificaciones (Telegram/email/push)
- P2: onboarding wizard, observabilidad avanzada y hardening de despliegue productivo

### Estimacion global: 55% completado

## 6. Lo que NO Hacer
- NO: hacer llamadas HTTP salientes sin pasar por `internal/ratelimit.NewHTTPClient`; rompe limites por dominio, backoff y user-agent unificado.
- NO: escribir secretos reales en archivos versionados (`.env`, `docker-compose.yml`, `deploy/helm/flux/values*.yaml`); usar secretos locales o Kubernetes Secret.
- NO: cambiar consultas que dependen de `metadata.source_ref` y `metadata.source_name` sin actualizar workers + API queries; se rompen filtros y estadisticas de fuentes.
- NO: asumir que `pending` significa lo mismo en todo el pipeline; puede ser backlog de processor (sin embedding/score) o cola de briefing (con score).
- NO: dejar `processor` o `embeddings-svc` caidos por periodos largos; con `ARTICLES MaxAge=72h` se pueden perder eventos y quedar articulos atascados en `pending`.
- NO: escalar solo workers de ingesta sin medir capacidad de `processor` y `embeddings-svc`; genera backlog creciente y briefings incompletos.
- NUNCA: modificar schema SQL sin migracion versionada en `migrations/`; la app aplica migraciones automaticamente y requiere trazabilidad.

## 7. Instrucciones para Codex
### Como arrancar el proyecto
```bash
git clone git@github.com:zyrak/flux.git
cd flux
cp .env.example .env
# editar al menos: LLM_API_KEY y POSTGRES_PASSWORD
docker compose up -d --build
# generar briefing inicial opcional
docker compose run --rm briefing-gen
```

### Como ejecutar tests
```bash
# backend
make test
# equivalente
go test -race -count=1 ./...

# frontend (typecheck/build)
cd web
npm ci
npm run check
npm run build
```

### Chequeos operativos inter-servicio
```bash
# Salud de infraestructura base
curl -sS http://localhost:8222/healthz    # NATS monitoring
curl -sS http://localhost:8080/healthz    # API health (postgres, redis, nats)
curl -sS http://localhost:8000/health     # embeddings-svc

# Backlog real del pipeline (PostgreSQL)
docker compose exec postgres psql -U flux -d flux -c \
"SELECT COUNT(*) AS pending_no_embedding FROM articles WHERE status='pending' AND embedding IS NULL;"

docker compose exec postgres psql -U flux -d flux -c \
"SELECT COUNT(*) AS pending_no_score FROM articles WHERE status='pending' AND embedding IS NOT NULL AND relevance_score IS NULL;"

docker compose exec postgres psql -U flux -d flux -c \
"SELECT COUNT(*) AS pending_ready_for_briefing FROM articles WHERE status='pending' AND relevance_score IS NOT NULL;"

docker compose exec postgres psql -U flux -d flux -c \
"SELECT COUNT(*) AS stale_over_72h FROM articles WHERE status='pending' AND ingested_at < NOW() - INTERVAL '72 hours';"

# Señales de bottleneck (logs)
docker compose logs --since=30m processor | rg -n \"Article processed|embedding|Error|failed|retry|backoff\" -S
docker compose logs --since=30m embeddings-svc | rg -n \"POST /embed|error|timeout\" -S
```
Interpretacion operativa:
- `pending_no_embedding` alto: processor atrasado o caido, o eventos no entregados.
- `pending_no_score` alto: cuello en embeddings/relevance dentro de processor.
- `stale_over_72h` > 0: riesgo de eventos expirados en NATS; requiere replay manual de `articles.new`.
- Si hay 429/403 en workers: revisar `RATE_LIMITS` y backoff en Redis antes de subir concurrencia.

### Rama base
Usar `main` como base. Crear rama `feature/{ticket}-{slug}` por tarea y rebasear antes de abrir PR.

### Ficheros que NO tocar
- `.env`: contiene credenciales locales; no versionar secretos
- `deploy/helm/flux/values.secrets.local.yaml`: archivo de secretos local (ignorado); no commitear
- `deploy/helm/flux/values.secrets.yaml`: reservado para secretos; evitar cambios con valores reales
- `embeddings-svc/__pycache__/main.cpython-312.pyc`: artefacto binario generado; no editar manualmente
- `tmp/deploy-k3s.sh`: script local con datos de infraestructura/credenciales de despliegue; no reutilizar ni versionar secretos derivados

### Formato esperado del output
- Cambios de codigo acotados al alcance de la tarea
- Tests nuevos o ajustados cuando cambie comportamiento
- Resumen tecnico de cambios y validacion ejecutada
- Commit message en Conventional Commits con scope del servicio afectado (`feat(processor): ...`, `fix(worker-hn): ...`, `ci: ...`)

### Cache de sesion
- Decisiones tomadas y por que
- Problemas encontrados y soluciones aplicadas
- Ficheros modificados
- Estado del trabajo si se pausa

## 8. Historial de Sesiones
### Sesion FLUX-AGENTS-20260227 - 2026-02-27
- Tarea: auditar repositorio completo y generar `AGENTS.md`
- Resultado: exito
- Que funciono: lectura de `cmd/`, `internal/`, `web/`, `deploy/`, `migrations/` y docs permitio reconstruir stack, arquitectura y estado real
- Que no funciono: no hay metadata explicita en codigo para identificar trabajo "en curso" ni convenciones no escritas
- Leccion: mantener `AGENTS.md` actualizado por sesion evita depender de inferencias desde codigo

### Sesion FLUX-AGENTS-20260227B - 2026-02-27
- Tarea: reforzar `AGENTS.md` con interacciones problematicas entre servicios y revisar `docs/` + `tmp/`
- Resultado: exito parcial
- Que funciono: se documentaron lag de processor, ventana de expiracion NATS (72h), cuellos de embeddings y runbook SQL/logs de deteccion
- Que no funciono: no hay historial formal de incidentes NATS en repo; solo evidencia indirecta en codigo/logging y commits de CI (GHCR)
- Leccion: registrar incidentes operativos reales en `docs/` para que futuros workers no dependan de inferencias

---
