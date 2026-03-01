# {NOMBRE_DEL_PROYECTO}
## Ultima actualizacion: {FECHA}

[Que rellenar: nombre oficial del proyecto y fecha en formato YYYY-MM-DD]
[Por que importa: evita ambiguedad de contexto y permite saber si la informacion esta vigente]
[Ejemplo: Proyecto: Flux | Ultima actualizacion: 2026-02-27]

## 1. Objetivo
[Que rellenar: 2-3 frases sobre que hace el proyecto, para quien, y por que existe]
[Por que importa: el worker decide prioridades tecnicas segun el objetivo de negocio]
[Evita: descripcion vaga tipo "app de noticias" sin usuario ni problema definido]

[Texto objetivo del proyecto]

Ejemplo:
"Flux es un sistema de agregacion de noticias potenciado por AI que recopila, clasifica y resume noticias de multiples fuentes RSS organizadas por secciones tematicas. Esta orientado a equipos que necesitan vigilancia informativa diaria sin leer cientos de fuentes manualmente. Existe para reducir tiempo de analisis y mejorar cobertura de temas criticos."

## 2. Stack Tecnologico
[Que rellenar: stack real en produccion, no stack deseado]
[Por que importa: el worker no debe proponer soluciones fuera de las restricciones del proyecto]
[Regla: incluir version cuando aplique]

- Lenguaje: [ej: Go 1.22]
- Framework: [ej: ninguno / gin / fiber]
- Base de datos: [ej: SQLite / PostgreSQL / ninguna]
- Dependencias clave: [lista con version]
- CI/CD: [ej: GitHub Actions / ninguno]
- Contenedorizacion: [ej: Docker + k3s / ninguno]
- Plataforma de despliegue: [ej: k3s homelab / Oracle Cloud]

Ejemplo:
- Lenguaje: Go 1.22.5
- Framework: net/http estandar (sin framework externo)
- Base de datos: SQLite 3.45
- Dependencias clave: github.com/mattn/go-sqlite3 v1.14.22, github.com/go-telegram-bot-api/telegram-bot-api/v5 v5.5.1
- CI/CD: GitHub Actions (build + test + docker)
- Contenedorizacion: Docker multi-stage
- Plataforma de despliegue: k3s homelab

## 3. Convenciones de Codigo
[Que rellenar: reglas operativas de desarrollo y estilo de codigo]
[Por que importa: reduce diffs innecesarios, regresiones y discusiones de estilo]
[Regla: si una convencion no existe, escribir "no definido" para evitar suposiciones]

- Formato: [ej: gofmt estandar]
- Naming: [ej: camelCase para variables, PascalCase para exports, snake_case para ficheros]
- Estructura de directorios: [describir patron, ej: cmd/ + internal/ + pkg/]
- Patron de commits: [ej: conventional commits - feat:, fix:, refactor:]
- Manejo de errores: [ej: errors.Wrap con contexto, no panic]
- Logging: [ej: slog estandar, structured logging]
- Tests: [ej: table-driven tests, _test.go junto al fichero]

Ejemplo:
- Formato: gofmt + goimports
- Naming: PascalCase para tipos exportados; camelCase para internos
- Estructura de directorios: cmd/orchestrator + internal/{planner,launcher,evaluator,state,llm,notify,dashboard}
- Patron de commits: conventional commits
- Manejo de errores: devolver error con contexto; panic solo en main para fallos fatales de arranque
- Logging: slog JSON con campos request_id, project_id, session_id
- Tests: table-driven + mocks pequenos por interfaz

## 4. Arquitectura
[Que rellenar: mapa del sistema y decisiones que no deben romperse]
[Por que importa: el worker puede cambiar implementacion sin romper principios base]

### 4.1 Descripcion de alto nivel
[Parrafo describiendo como encajan los componentes principales]

Ejemplo:
"El orquestador coordina planificacion, lanzamiento de workers y evaluacion. El estado persistente vive en SQLite. El dashboard consulta endpoints REST del mismo binario para mostrar estado y progreso. Telegram notifica eventos y acepta comandos de control."

### 4.2 Componentes
[Lista de componentes con una frase cada uno]

- [Componente]: [Responsabilidad principal]
- [Componente]: [Responsabilidad principal]
- [Componente]: [Responsabilidad principal]

Ejemplo:
- planner: descompone directivas en tareas ejecutables usando GLM
- launcher: crea y monitorea workers Codex por rama Git
- evaluator: califica diffs y valida criterios de aceptacion
- state: encapsula acceso SQLite y migraciones
- dashboard: expone REST y sirve vista web
- notify/telegram: envia alertas y procesa comandos operativos

### 4.3 Decisiones de arquitectura
[Formato requerido: "Decision: X. Razon: Y. Alternativa descartada: Z."]
[Que rellenar: decisiones estables y sus tradeoffs]
[Por que importa: evita reabrir debates ya cerrados en cada tarea]

- Decision 1: [Decision: ... Razon: ... Alternativa descartada: ...]
- Decision 2: [Decision: ... Razon: ... Alternativa descartada: ...]

Ejemplo:
- Decision 1: Decision: binario unico en Go. Razon: despliegue simple y menor complejidad operacional. Alternativa descartada: microservicios por componente.
- Decision 2: Decision: SQLite como fuente de verdad inicial. Razon: cero infraestructura externa y recuperacion sencilla. Alternativa descartada: PostgreSQL desde dia 1.

### 4.4 Diagrama (opcional)
[ASCII art o descripcion textual del flujo de datos]

Ejemplo:
`Usuario -> Orchestrator -> Planner(GLM) -> Launcher(Codex Worker) -> Evaluator(GLM) -> State(SQLite) -> Dashboard/Telegram`

## 5. Estado Actual
[Que rellenar: estado real del backlog, no roadmap aspiracional]
[Por que importa: permite al worker elegir la siguiente tarea util sin bloquearse]

### Completado
- [Lista de features/modulos terminados]

Ejemplo:
- Inicializacion de proyecto y estructura de paquetes completada
- Schema SQLite definido con estados y relaciones
- Endpoints base del dashboard creados (stubs)

### En Curso
- [Lista de lo que se esta trabajando ahora]

Ejemplo:
- Implementacion del planner contra API GLM
- Integracion de launcher con procesos Codex reales

### Pendiente
- [Lista de lo que falta, ordenado por prioridad]

Ejemplo:
- P0: persistencia real en SQLite
- P1: comandos Telegram start/status/pause/resume
- P1: evaluacion automatica de calidad por criterios
- P2: mejoras de UX del dashboard

### Estimacion global: [X]% completado

Ejemplo:
### Estimacion global: 35% completado

## 6. Lo que NO Hacer
[Seccion critica. Registrar errores historicos para no repetirlos]
[Que rellenar: anti-patrones concretos con contexto tecnico]
[Por que importa: ahorra ciclos y reduce regresiones repetidas]

- NO: [error especifico y por que]
- NO: [otro error]
- NUNCA: [restriccion fuerte]

Ejemplo:
- NO: usar goroutines para llamadas RSS en paralelo sin control de orden; genero race conditions en parser y resultados inconsistentes.
- NO: mezclar logica de negocio en handlers HTTP; dificulta pruebas unitarias y rompe separacion de capas.
- NUNCA: modificar el schema de BD sin migracion versionada; hay datos de produccion que deben preservarse.

## 7. Instrucciones para Codex
[Que rellenar: protocolo operativo exacto para que el worker ejecute tareas sin preguntas]
[Por que importa: baja latencia de ejecucion y menos errores de entorno]

### Como arrancar el proyecto
```bash
[comandos exactos para clonar, instalar deps, y arrancar]
```

Ejemplo:
```bash
git clone git@github.com:zyrak/hivemind.git
cd hivemind
go mod tidy
make run
```

### Como ejecutar tests
```bash
[comandos exactos]
```

Ejemplo:
```bash
go test ./...
go vet ./...
```

### Rama base
[ej: usar siempre `main` como base. Crear rama `feature/{tarea}` para cada tarea.]

Ejemplo:
Usar `main` como base. Crear `feature/{ticket}-{slug}` por tarea. Rebase antes de abrir PR.

### Ficheros que NO tocar
[lista de ficheros que no deben modificarse y por que]

Ejemplo:
- `deploy/secrets.yaml`: contiene nombres de secretos operativos; no versionar cambios con valores reales.
- `sessions/cache/*.md`: artefactos de sesion; no editar sesiones de otros workers.

### Formato esperado del output
[que debe entregar el worker al terminar: codigo + tests + commit message]

Ejemplo:
- Cambios de codigo acotados al alcance de la tarea
- Tests nuevos o ajustados
- Resumen tecnico de cambios
- Commit message en formato conventional commit

### Cache de sesion
[El worker debe mantener un fichero `sessions/cache/{session-id}.md` con:]
- Decisiones tomadas y por que
- Problemas encontrados y soluciones aplicadas
- Ficheros modificados
- Estado del trabajo si se pausa

Ejemplo:
- `sessions/cache/ses-20260227-1130.md` con decisiones de arquitectura, comandos ejecutados y pendientes para retomar.

## 8. Historial de Sesiones
[Que rellenar: resumen corto por sesion, una entrada por bloque]
[Por que importa: permite continuidad entre workers/modelos sin releer todo el repo]

### Sesion [ID] - [Fecha]
- Tarea: [que se pidio]
- Resultado: [exito/parcial/fallo]
- Que funciono: [breve]
- Que no funciono: [breve]
- Leccion: [que se aprendio]

Ejemplo:
### Sesion Hivemind-142 - 2026-02-26
- Tarea: crear migraciones iniciales de SQLite
- Resultado: exito
- Que funciono: separar schema y capa state simplifico pruebas
- Que no funciono: primer intento con SQL inline duplicado en tests
- Leccion: centralizar schema en `migrations.go` y reutilizar en tests

---
[Uso recomendado: copiar esta plantilla a `AGENTS.md`, rellenar todos los bloques y eliminar ejemplos que ya no aporten contexto.] 
