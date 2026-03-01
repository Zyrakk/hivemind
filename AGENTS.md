# Hivemind
## Ultima actualizacion: 2026-02-28

## 1. Objetivo
Hivemind es un orquestador de desarrollo semi-autonomo que planifica, ejecuta y evalua tareas de codigo usando workers Codex en ramas Git dedicadas. Esta orientado a equipos que quieren acelerar delivery con control de calidad y trazabilidad. Existe para convertir directrices de alto nivel en cambios verificables con contexto persistente.

## 2. Stack Tecnologico
- Lenguaje: Go 1.22
- Framework: net/http estandar
- Base de datos: SQLite
- Dependencias clave: stdlib (fase bootstrap)
- CI/CD: no definido
- Contenedorizacion: Docker multi-stage
- Plataforma de despliegue: Kubernetes (manifests en `deploy/`)

## 3. Convenciones de Codigo
- Formato: gofmt estandar
- Naming: camelCase para variables internas, PascalCase para exports, snake_case para archivos no-Go
- Estructura de directorios: `cmd/` + `internal/` + `agents/` + `prompts/` + `deploy/`
- Patron de commits: conventional commits
- Manejo de errores: devolver `error` con contexto; evitar `panic` fuera de arranque
- Logging: `log`/`slog` estructurado
- Tests: `_test.go` junto al paquete

## 4. Arquitectura
### 4.1 Descripcion de alto nivel
El binario orquestador coordina planner, launcher, evaluator, state, dashboard y notificaciones. Planner y evaluator usan GLM como LLM principal. State guarda progreso en SQLite. Dashboard y Telegram sirven para observabilidad y control.

### 4.2 Componentes
- planner: descompone directrices en tareas atomicas
- launcher: crea y monitorea workers Codex
- evaluator: evalua diffs segun criterios
- state: persistencia SQLite y migraciones
- llm: clientes GLM + consultores opcionales Claude/Gemini
- dashboard: endpoints REST para estado/progreso/contexto
- notify: notificaciones y comandos por Telegram

### 4.3 Decisiones de arquitectura
- Decision 1: binario unico en Go. Razon: simplicidad operativa. Alternativa descartada: microservicios.
- Decision 2: SQLite en fase inicial. Razon: despliegue sin dependencias externas. Alternativa descartada: PostgreSQL desde el inicio.
- Decision 3: prompts en archivos de texto versionados. Razon: trazabilidad y ajuste rapido. Alternativa descartada: prompts embebidos en codigo.

## 5. Estado Actual
### Completado
- Estructura completa del repositorio Hivemind creada
- Stubs de paquetes internos y tests base creados
- Schema SQLite definido con tablas y enums por `CHECK`
- Prompts planner/evaluator/consultant definidos con formato JSON documentado

### En Curso
- Implementacion real de planner, launcher, evaluator y capa state

### Pendiente
- Integracion real con API GLM/Claude/Gemini
- Ejecucion real de workers Codex en ramas dedicadas
- Persistencia operativa y panel con datos en vivo

### Estimacion global: 35% completado

## 6. Lo que NO Hacer
- NO introducir secrets reales en archivos del repo.
- NO romper contrato JSON de prompts (el orquestador parsea estructuras fijas).
- NO modificar schema de base sin migracion versionada.
- NUNCA mezclar cambios fuera del scope de la tarea en una sola sesion.

## 7. Instrucciones para Codex
### Como arrancar el proyecto
```bash
go mod tidy
make run
```

### Como ejecutar tests
```bash
go test ./...
go vet ./...
```

### Rama base
Usar `main` como base y crear `feature/{tarea}` por sesion.

### Ficheros que NO tocar
- `deploy/secrets.yaml`: nunca versionar secretos reales.
- `sessions/cache/*.md`: no editar cache de otras sesiones.

### Formato esperado del output
- Codigo + tests (si aplica) + resumen tecnico.
- Commit message en formato conventional commit.

### Cache de sesion
Cada worker debe mantener `sessions/cache/{session-id}.md` con decisiones, problemas, archivos modificados y estado de pausa.

## 8. Historial de Sesiones
### Sesion Hivemind-001 - 2026-02-27
- Contexto usado: `AGENTS.md` (version bootstrap)
- Tarea: crear estructura inicial de carpetas y paquetes
- Resultado: exito
- Que funciono: separacion por `internal/*` simplifico bootstrap
- Que no funciono: faltaba `agents/vuln-reporter.md`
- Leccion: validar checklist completa antes de cerrar fase

### Sesion Hivemind-002 - 2026-02-27
- Contexto usado: `AGENTS.md` (version bootstrap)
- Tarea: definir schema SQLite y enums de estado
- Resultado: exito
- Que funciono: `CHECK` constraints documentan estados validos
- Que no funciono: sin validacion runtime aun
- Leccion: primero contrato de datos, despues logica

### Sesion Hivemind-003 - 2026-02-27
- Contexto usado: `AGENTS.md` (version bootstrap)
- Tarea: crear prompts estructurados para planner/evaluator/consultant
- Resultado: exito
- Que funciono: formato JSON fijo facilita parsing automatico
- Que no funciono: hubo inconsistencias de naming de sistema
- Leccion: fijar nombre de sistema en prompts desde el inicio

### Sesion Hivemind-004 - 2026-02-28
- Contexto usado: `AGENTS.md` (version bootstrap)
- Tarea: validar build y vet del repositorio
- Resultado: exito
- Que funciono: `go build` y `go vet` con cache local
- Que no funciono: cache global de Go con permisos restringidos
- Leccion: fijar `GOCACHE` local en entornos aislados

### Sesion Hivemind-005 - 2026-02-28
- Contexto usado: `AGENTS.md` (version actual)
- Tarea: cerrar gaps de Fase 0 y actualizar evidencia de calidad
- Resultado: exito
- Que funciono: consolidar historial + metricas en artefactos versionados
- Que no funciono: validacion Docker depende de acceso a daemon
- Leccion: mantener verificaciones reproducibles fuera de dependencias de entorno

## 9. KPI de Calidad Codex
Fuente: `sessions/codex_runs.csv`
- Ejecuciones medidas: 10
- Outputs aceptables: 8
- Tasa de aceptacion: 80%
- Umbral requerido: 70%
- Estado: CUMPLE
