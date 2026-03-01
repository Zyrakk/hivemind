# ZPlay
## Ultima actualizacion: 2026-02-27

## 1. Objetivo
ZPlay es una CLI para desplegar, operar y mantener servidores de juegos en Kubernetes (k3s) sin exponer al operador a la complejidad de manifiestos y comandos manuales repetitivos. Esta orientado a operadores tecnicos que necesitan gestionar servidores de Terraria de forma confiable, tanto en modo interactivo como por subcomandos automatizables. El proyecto existe para estandarizar operaciones de ciclo de vida (deploy, start/stop, status, backup, restore, delete, logs, console) y reducir errores operativos en cluster.

## 2. Stack Tecnologico
- Lenguaje: Go 1.22
- Framework: CLI/TUI en Bubble Tea v1.2.4 + Lip Gloss v1.0.0 (sin framework web)
- Base de datos: ninguna; estado local en YAML (`~/.zplay/servers.yaml`)
- Dependencias clave: github.com/charmbracelet/bubbletea v1.2.4, github.com/charmbracelet/lipgloss v1.0.0, gopkg.in/yaml.v3 v3.0.1
- CI/CD: no definido (no hay workflows de CI/CD en el repositorio)
- Contenedorizacion: despliegue en Kubernetes con templates YAML embebidos; imagenes Docker de terceros por juego
- Plataforma de despliegue: Kubernetes k3s (entorno ZCloud), ingreso TCP con Traefik

## 3. Convenciones de Codigo
- Formato: `go fmt` estandar (Makefile incluye `make fmt`)
- Naming: convencion Go (PascalCase para exportados, camelCase para internos); nombres de ficheros en minusculas
- Estructura de directorios: `cmd/zplay` (entrada) + `internal/{cli,config,games,k8s}` + templates K8s por juego en `internal/games/<juego>/templates`
- Patron de commits: no definido; historial mixto (`fix:`, `docs:`, y commits por fase en texto libre)
- Manejo de errores: retorno explicito de `error` con contexto (`fmt.Errorf(... %w ...)`); salida en `main` con `os.Exit`
- Logging: no definido como subsistema dedicado; salida por consola con `fmt.Println`/`fmt.Printf` y estilos de TUI
- Tests: tests unitarios en `_test.go` junto al codigo (actualmente cobertura concentrada en `internal/config/config_kubeconfig_test.go`)

## 4. Arquitectura
### 4.1 Descripcion de alto nivel
El binario `zplay` ofrece dos modos de operacion: menu interactivo y subcomandos directos. Ambos construyen configuracion de servidor, delegan validacion/renderizado al modulo de juego (hoy Terraria), aplican manifiestos sobre Kubernetes via wrapper de `kubectl`, y sincronizan estado local en YAML. Para robustez operativa, el comando de listado reconcilia estado local con estado real del cluster y puede adoptar o limpiar entradas.

### 4.2 Componentes
- `cmd/zplay`: parsing de argumentos, dispatch de subcomandos y arranque interactivo.
- `internal/cli`: flujos de usuario para deploy, list, start/stop, status, backup, restore, delete, console y logs.
- `internal/config`: carga/guardado de config y estado local, resolucion de kubeconfig y reconciliacion.
- `internal/games`: contrato `Game`, registry de juegos y mapeo puerto->entrypoint.
- `internal/games/terraria`: implementacion concreta de Terraria (vanilla/tmodloader), validaciones y render de templates.
- `internal/k8s`: wrapper de operaciones de `kubectl`/`zcloud k` para aplicar, escalar, inspeccionar, ejecutar jobs y obtener metricas.

### 4.3 Decisiones de arquitectura
- Decision 1: Decision: CLI unica con modo interactivo y modo no interactivo en el mismo binario. Razon: reduce friccion operativa y permite uso humano y automatizado sin duplicar codigo. Alternativa descartada: separar una API/servicio adicional para operaciones.
- Decision 2: Decision: extensibilidad por interfaz `Game` + registro dinamico por `init()`. Razon: permite agregar nuevos juegos sin reescribir la orquestacion principal. Alternativa descartada: logica condicional hardcodeada por juego dentro de handlers CLI.
- Decision 3: Decision: operar Kubernetes via wrapper de `kubectl` (y `zcloud k` cuando aplica), no via client-go. Razon: simplicidad, menor superficie de mantenimiento y compatibilidad directa con tooling existente del entorno. Alternativa descartada: integrar `client-go` y gestionar autenticacion/objetos desde SDK.
- Decision 4: Decision: mantener estado local en YAML y reconciliar contra cluster desde `list`. Razon: UX rapida en CLI sin perder coherencia con la fuente de verdad real (cluster). Alternativa descartada: tratar `servers.yaml` como fuente unica de verdad.

### 4.4 Diagrama (opcional)
`Usuario -> zplay (interactive/subcommands) -> game registry (Game) -> render templates -> k8s client (kubectl/zcloud k) -> cluster k3s -> estado local (~/.zplay/servers.yaml)`

## 5. Estado Actual
### Completado
- CLI interactiva completa para deploy/list/start-stop/status/backup/restore/delete/console/logs.
- Modo no interactivo para `deploy`, `list`, `delete`, `start`, `stop`, `backup`, `status`.
- Reconciliacion interactiva de estado local con cluster (adopcion y limpieza).
- Status detallado por servidor con fallback a `N/A` cuando faltan metricas.
- Soporte Terraria vanilla y tModLoader (con restriccion a nodo x86 `lake` para tModLoader).
- Backup manual, auto-backup por CronJob y flujo de restore.
- Resolucion robusta de kubeconfig (config local, env, `~/.kube/config`, fallback legacy).

### En Curso
- No hay item explicitamente marcado en curso dentro del repositorio; el siguiente bloque priorizado es Fase 5 (Minecraft) segun `docs/roadmap.md`.

### Pendiente
- P0: implementar soporte productivo de Minecraft (`internal/games/minecraft/`, templates y registro).
- P1: variantes Minecraft iniciales (`vanilla`, `paper`, `forge`) con validaciones por variante.
- P1: paridad operativa completa para Minecraft (start/stop/status + backup/restore + tests).
- P1: `zplay list --sync` no interactivo para reconciliacion.
- P1: subcomando no interactivo para restore (`zplay restore <name> --backup <file>`).
- P2: salida estructurada adicional (`status --json`).
- P2: pruebas E2E automatizadas sobre entorno k3s de staging.

### Estimacion global: 70% completado

## 6. Lo que NO Hacer
- NO: desplegar Terraria en puertos fuera de los entrypoints soportados (`7777`, `7778`); rompe enrutamiento TCP esperado en Traefik.
- NO: desplegar `tmodloader` fuera del nodo `lake`; esta variante requiere arquitectura x86 y ya existe validacion explicita.
- NO: volver a probes TCP para Terraria; se cambio a probes `exec` para evitar spam de conexiones y ruido en logs.
- NO: asumir que `~/.zplay/servers.yaml` refleja siempre la realidad del cluster; ejecutar reconciliacion en `list` y sincronizar metadatos.
- NUNCA: almacenar passwords en plaintext dentro de Deployment; usar `Secret` y `secretKeyRef`.

## 7. Instrucciones para Codex
### Como arrancar el proyecto
```bash
git clone https://github.com/Zyrakk/zplay.git
cd zplay
make deps
make dev
```

### Como ejecutar tests
```bash
go test ./...
go build ./cmd/zplay
make test
```

### Rama base
Usar siempre `main` como base. Crear rama `feature/{ticket-o-fecha}-{slug}` por tarea y rebasear antes de abrir PR.

### Ficheros que NO tocar
- `dist/zplay`: binario generado; se regenera con `make build`.
- `tmp/prompts.md`: historial de prompts y procedimiento de fases, no fuente de verdad de runtime.
- `tmp/phase2helper/main.go` y `tmp/phase3helper/main.go`: helpers temporales de pruebas/manuales.
- `VERSION`: solo actualizar en cambios de version planificados.

### Formato esperado del output
- Cambios de codigo acotados al alcance de la tarea
- Tests nuevos o ajustados cuando aplique
- Resumen tecnico de cambios y riesgos
- Si se hace commit, mensaje claro (idealmente estilo conventional commit)

### Cache de sesion
El worker debe mantener un fichero `sessions/cache/{session-id}.md` con:
- Decisiones tomadas y por que
- Problemas encontrados y soluciones aplicadas
- Ficheros modificados
- Estado del trabajo si se pausa

## 8. Historial de Sesiones
### Sesion ZPLAY-20260219-PH2-PH3 - 2026-02-19
- Tarea: implementar tModLoader y luego backup/restore
- Resultado: exito
- Que funciono: extender `Game` con jobs de backup/restore y plantillas dedicadas
- Que no funciono: suponer compatibilidad de tModLoader en ARM
- Leccion: fijar restricciones de arquitectura por variante y validarlas en deploy

### Sesion ZPLAY-20260220-PH4 - 2026-02-20
- Tarea: mejoras de usabilidad (reconciliacion, CLI flags, status detallado, kubeconfig fallback)
- Resultado: exito
- Que funciono: separar modo interactivo y no interactivo reutilizando funciones comunes
- Que no funciono: depender del path legacy de kubeconfig como unica opcion
- Leccion: mantener cadena de fallback de kubeconfig y tolerancia a metricas ausentes (`N/A`)

### Sesion ZPLAY-20260225-HARDEN - 2026-02-25
- Tarea: hardening de consola/logs y sincronizacion de metadatos
- Resultado: exito
- Que funciono: `tmux` para detach limpio y `stty sane` tras Bubble Tea
- Que no funciono: attach directo sin tmux y probes anteriores generaron friccion operativa
- Leccion: priorizar ergonomia de terminal y evitar comportamientos que degraden logs/UX
