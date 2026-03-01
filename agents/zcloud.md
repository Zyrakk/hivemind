# ZCloud
## Ultima actualizacion: 2026-02-28

Proyecto oficial: ZCloud (CLI + server para gestion remota de k3s).

## 1. Objetivo
ZCloud es un sistema cliente-servidor para operar clusters k3s remotos de forma segura desde cualquier Linux. Esta orientado a operadores/homelabers que necesitan administrar Kubernetes sin exponer kubeconfigs ni puertos del cluster. Existe para centralizar autenticacion fuerte (device key + TOTP + JWT), controlar acceso por dispositivo y habilitar operaciones remotas (kubectl proxy, apply, SSH, port-forward y transferencia de archivos).

## 2. Stack Tecnologico
- Lenguaje: Go 1.22 (`go.mod`)
- Framework: `net/http` estandar + `github.com/spf13/cobra` v1.8.0 para CLI
- Base de datos: SQLite via `modernc.org/sqlite` v1.28.0
- Dependencias clave: `github.com/golang-jwt/jwt/v5` v5.2.0, `github.com/gorilla/websocket` v1.5.1, `github.com/pquerna/otp` v1.4.0, `github.com/skip2/go-qrcode` (commit `da1b6568686e`), `github.com/google/uuid` v1.5.0
- CI/CD: no definido (no hay `.github/workflows` en el repo)
- Contenedorizacion: no definido (sin Dockerfiles/Helm charts en este repo)
- Plataforma de despliegue: servidor Linux (systemd) + cluster k3s remoto/homelab (README menciona host N150)

## 3. Convenciones de Codigo
- Formato: estilo Go idiomatico; `gofmt` implicito (no se define herramienta extra en repo)
- Naming: PascalCase para exportados, camelCase para internos, archivos en minuscula con `_` cuando aplica (`k8s_proxy.go`)
- Estructura de directorios: `cmd/{zcloud,zcloud-server}` + `internal/{client,server,shared}` + `configs/` + `scripts/`
- Patron de commits: no definido (historial mixto; parcialmente conventional commits)
- Manejo de errores: retorno explicito de `error` con contexto (`fmt.Errorf`), `panic` no usado, `log.Fatalf` solo en arranque server
- Logging: `log` estandar + eventos de auditoria con prefijo `AUDIT`
- Tests: tests unitarios con `testing` en archivos `_test.go` junto al paquete; sin framework externo de testing

## 4. Arquitectura
### 4.1 Descripcion de alto nivel
El binario `zcloud` gestiona identidad local (claves Ed25519), sesion y comandos operativos; el binario `zcloud-server` expone una API REST/WebSocket protegida por JWT y TOTP. El estado persistente (dispositivos, usuarios, sesiones, revocaciones, codigos de enrolamiento) vive en SQLite. El server tambien actua como pasarela hacia k3s (proxy Kubernetes, ejecucion de comandos permitidos, SSH interactivo, port-forward y archivos) con validaciones de seguridad y middleware de auth/rate limit.

### 4.2 Componentes
- `cmd/zcloud`: CLI de usuario final (init, login, status, k, apply, ssh, port-forward, cp, admin).
- `cmd/zcloud-server`: servidor API, bootstrap/config y CLI administrativa local (`admin`).
- `internal/client`: flujos de autenticacion cliente, HTTP API client, kubeconfig, SSH/port-forward, archivos.
- `internal/server/api`: handlers REST/WS, proxy k8s y operaciones remotas.
- `internal/server/middleware`: JWT auth, control admin, rate limiting, headers de seguridad, logging.
- `internal/server/db`: capa SQLite y esquema de persistencia.
- `internal/shared/crypto`: Ed25519, TOTP y utilidades criptograficas compartidas.
- `internal/shared/protocol`: contratos de requests/responses y mensajes WS.

### 4.3 Decisiones de arquitectura
- Decision 1: Decision: dos binarios Go en un monolito logico (`zcloud` y `zcloud-server`). Razon: despliegue y operacion simples con baja complejidad. Alternativa descartada: microservicios separados por capability.
- Decision 2: Decision: SQLite como fuente de verdad para server state. Razon: cero infraestructura externa y facil backup/restore en host unico. Alternativa descartada: PostgreSQL desde el inicio.
- Decision 3: Decision: autenticacion multinivel (device key + TOTP + JWT revocable). Razon: reduce riesgo de robo de token y permite revocacion inmediata. Alternativa descartada: API token estatico por cliente.
- Decision 4: Decision: endpoint `/api/v1/k8s/proxy/*` sin rate limiting, pero autenticado. Razon: compatibilidad con Helm/kubectl en alta concurrencia. Alternativa descartada: aplicar mismo rate limit global y romper operaciones paralelas.
- Decision 5: Decision: TOTP por usuario/persona con enrolamiento one-time por codigo efimero. Razon: compartir TOTP entre multiples dispositivos del mismo usuario sin exponer secreto por endpoints publicos. Alternativa descartada: TOTP por dispositivo con secreto reutilizable.
- Decision 6: Decision: notificaciones de dispositivos pendientes diferidas hasta definir una interfaz de notifier y requisitos operativos. Razon: hoy solo existe auditoria por logs y no hay contrato/config para integraciones externas. Alternativa descartada: acoplar directamente Telegram en `handlers.go`.

### 4.4 Diagrama (opcional)
`Usuario -> zcloud CLI -> HTTPS/WS -> zcloud-server API -> middleware(auth/rate/security) -> db(SQLite) + k3s API/host tools`

## 5. Estado Actual
### Completado
- Registro/aprobacion/revocacion de dispositivos con auditoria.
- Login/logout con TOTP + firma Ed25519 + JWT con revocacion.
- Enrolamiento TOTP one-time por usuario/persona.
- Proxy Kubernetes autenticado (`/api/v1/k8s/proxy/*`) con pooling de conexiones.
- Comandos operativos remotos: `apply`, `exec` (whitelist), `ssh`, `port-forward`, `cp` (upload/download/list/delete).
- CLI de administracion de dispositivos y rotacion de TOTP por usuario.
- Health/readiness checks.
- Suite de tests en `db`, `middleware`, `shared/crypto` pasando en `go test ./...` (ejecutado el 2026-02-27).

### En Curso
- Integracion de notificacion administrativa al registrar dispositivos pendientes (hay `TODO` en `internal/server/api/handlers.go`).
- Contexto del TODO (derivado del repo): no existe todavia una abstraccion `notifier`, ni configuracion de canal/credenciales en `ServerConfig`; por eso no se implemento aun sin acoplar el API a un proveedor concreto.
- Estado de decision de canal: no cerrado en codigo/documentacion. Solo se menciona "Telegram, etc." como opcion en comentario; no hay una decision final versionada.

### Pendiente
- P0: definir y automatizar test de integracion del flujo completo de auth (device key + TOTP enroll + login JWT + revocacion/logout).
- P1: implementar canal de notificacion real para aprobaciones pendientes (email/telegram/etc.).
- P1: formalizar configuracion de desarrollo local (`configs/dev-config.yaml` no versionado, pero referenciado por `make dev-server`).
- P2: ampliar cobertura de tests para `internal/server/api` e `internal/client`.
- P2: definir pipeline CI/CD automatizado (build + test + releases).

### Estimacion global: 85% completado

## 6. Lo que NO Hacer
- NO: volver a aplicar rate limiting sobre `/api/v1/k8s/proxy/*`; ya provoco incompatibilidades con Helm por llamadas paralelas.
- NO: usar `InsecureSkipVerify` para conexion server->k8s; la implementacion actual valida CA y solo debe relajarse con justificacion explicita.
- NO: exponer secretos TOTP en endpoints de estado o registro; el secreto solo debe salir una vez via flujo `totp/enroll`.
- NO: mezclar comandos arbitrarios en `handleExec`; mantener whitelist explicita (`kubectl`, `helm`, `k3s`).
- NUNCA: cambiar schema de SQLite sin migracion compatible (la tabla `devices` ya tuvo migracion ligera para `user_id`).

## 7. Instrucciones para Codex
### Como arrancar el proyecto
```bash
git clone https://github.com/zyrak/zcloud
cd zcloud
make deps
make build

# Cliente en local
go run ./cmd/zcloud --help

# Server en local/host objetivo (requiere config valida)
go run ./cmd/zcloud-server --config /opt/zcloud-server/config.yaml
```

### Como ejecutar tests
```bash
go test ./...
go test ./internal/server/db/...
go test ./internal/server/middleware/...
go test ./internal/shared/crypto/...
```

### Protocolo de regresion auth (obligatorio si tocas auth)
```bash
# 0) Preparar entorno aislado para no pisar ~/.zcloud
export ZCLOUD_E2E_DIR=/tmp/zcloud-e2e
rm -rf "$ZCLOUD_E2E_DIR"

# 1) Registrar dispositivo
go run ./cmd/zcloud --config-dir "$ZCLOUD_E2E_DIR" init https://api.zyrak.cloud
DEVICE_ID=$(awk '/^  id: / {print $2; exit}' "$ZCLOUD_E2E_DIR/config.yaml")

# 2) Aprobar en server y asignar user (capturar enrollment code)
go run ./cmd/zcloud-server admin devices approve "$DEVICE_ID" --user e2e-user --config /opt/zcloud-server/config.yaml
# Guardar el enrollment code que imprime el comando

# 3) Completar init + enrolar TOTP (flujo one-time)
go run ./cmd/zcloud --config-dir "$ZCLOUD_E2E_DIR" init --complete
go run ./cmd/zcloud --config-dir "$ZCLOUD_E2E_DIR" totp <ENROLLMENT_CODE>
# Verificar que reutilizar el MISMO enrollment code falla (one-time)
go run ./cmd/zcloud --config-dir "$ZCLOUD_E2E_DIR" totp <ENROLLMENT_CODE>

# 4) Login y comprobacion de sesion JWT
go run ./cmd/zcloud --config-dir "$ZCLOUD_E2E_DIR" login
go run ./cmd/zcloud --config-dir "$ZCLOUD_E2E_DIR" status --check-only

# 5) Logout y revocacion efectiva
TOKEN=$(awk '/^  token: / {print $2; exit}' "$ZCLOUD_E2E_DIR/config.yaml")
go run ./cmd/zcloud --config-dir "$ZCLOUD_E2E_DIR" logout
HTTP_CODE=$(curl -sk -o /dev/null -w "%{http_code}" -H "Authorization: Bearer $TOKEN" https://api.zyrak.cloud/api/v1/status/cluster)
test "$HTTP_CODE" = "401"

# 6) Revoke device y verificar bloqueo de re-login
go run ./cmd/zcloud-server admin devices revoke "$DEVICE_ID" --config /opt/zcloud-server/config.yaml
go run ./cmd/zcloud --config-dir "$ZCLOUD_E2E_DIR" login
```
- Criterios de aceptacion minimos:
- `totp` con codigo ya usado debe fallar.
- `login` exitoso crea sesion valida.
- tras `logout`, el JWT previo debe responder `401` en endpoint protegido.
- tras `revoke`, el dispositivo no debe poder iniciar sesion.
- Si cambias `handleLogin`, `handleLogout`, `handleTOTPEnroll`, middleware JWT o tablas de sesiones/revocacion: ejecutar este protocolo completo + `go test ./...`.

### Rama base
Usar `main` como base. Crear rama `feature/{ticket}-{slug}` por tarea y rebasear antes de abrir PR.

### Ficheros que NO tocar
- `VERSION`: solo actualizar en proceso de release.
- `configs/zcloud-server.service`: mantener hardening de systemd; cambios solo si hay justificacion operativa.
- `go.sum`: no editar manualmente (solo via `go mod tidy`/cambios reales de dependencias).

### Formato esperado del output
- Cambios de codigo acotados al alcance de la tarea.
- Tests nuevos o ajustados cuando se modifique logica.
- Resumen tecnico corto con riesgos y validaciones ejecutadas.
- Commit message preferido en formato conventional commit (`feat:`, `fix:`, `chore:`).

### Cache de sesion
El worker debe mantener `sessions/cache/{session-id}.md` con:
- Decisiones tomadas y por que.
- Problemas encontrados y soluciones aplicadas.
- Ficheros modificados.
- Estado del trabajo si se pausa.

## 8. Historial de Sesiones
### Sesion ZCLOUD-AGENTS-20260228 - 2026-02-28
- Tarea: reforzar AGENTS con protocolo de pruebas E2E de auth y contexto del TODO de notificaciones.
- Resultado: exito.
- Que funciono: documentar un checklist ejecutable del flujo device key + TOTP + JWT + revocacion.
- Que no funciono: no hay decision final de canal de notificaciones versionada en el repo.
- Leccion: cualquier cambio de auth debe requerir validacion E2E explicita, no solo tests unitarios.

### Sesion ZCLOUD-AGENTS-20260227 - 2026-02-27
- Tarea: auditar el repositorio completo y generar `AGENTS.md`.
- Resultado: exito.
- Que funciono: lectura integral de codigo/config/tests + verificacion con `go test ./...`.
- Que no funciono: no se puede inferir del codigo el contexto humano de decisiones recientes en curso.
- Leccion: mantener en `AGENTS.md` una seccion explicita para contexto no inferible y actualizarla por sesion.

---
