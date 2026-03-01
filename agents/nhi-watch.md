# NHI-Watch
## Ultima actualizacion: 2026-02-27

## 1. Objetivo
NHI-Watch es una CLI de seguridad para Kubernetes/OpenShift que descubre identidades no humanas (ServiceAccounts, Secrets, certificados TLS y certificados de cert-manager), resuelve sus permisos RBAC efectivos y calcula un riesgo determinista. Esta orientado a equipos de plataforma y seguridad que necesitan auditar posture de workload identity sin depender de soluciones cerradas. Existe para reducir puntos ciegos de credenciales de maquina y priorizar remediacion con hallazgos reproducibles.

## 2. Stack Tecnologico
- Lenguaje: Go 1.22.0 (CI tambien valida Go 1.23)
- Framework: CLI con Cobra v1.8.1 (sin framework web)
- Base de datos: ninguna
- Dependencias clave: github.com/spf13/cobra v1.8.1, k8s.io/client-go v0.31.1, k8s.io/api v0.31.1, k8s.io/apimachinery v0.31.1
- CI/CD: GitHub Actions (`.github/workflows/ci.yml`) con jobs de lint, test y build
- Contenedorizacion: no definido (no hay Dockerfile en el repo)
- Plataforma de despliegue: ejecucion como CLI local o in-cluster usando kubeconfig/client-go

## 3. Convenciones de Codigo
- Formato: gofmt + goimports (configurado en `.golangci.yml`)
- Naming: PascalCase para tipos/exportados, camelCase para internos, nombres de ficheros en minusculas
- Estructura de directorios: `cmd/nhi-watch` + `internal/{cli,discovery,k8s,models,permissions,scoring,reporter}` + `configs` + `scripts`
- Patron de commits: no definido (historial mixto entre `fix:` y mensajes libres/versionados)
- Manejo de errores: `fmt.Errorf(...: %w)` con contexto; no `panic` en flujo normal (salida controlada desde `main`)
- Logging: no definido como framework; salida operativa por `stderr` y modo `--verbose`
- Tests: tests unitarios en `*_test.go` junto al codigo, uso de `testing` estandar y `k8s.io/client-go/kubernetes/fake`

## 4. Arquitectura
### 4.1 Descripcion de alto nivel
El binario `nhi-watch` ejecuta tres flujos principales: descubrimiento de NHIs, resolucion RBAC y scoring determinista. `discover` corre fases en paralelo (ServiceAccounts, Secrets y cert-manager via cliente dinamico), `permissions` resuelve el grafo RBAC completo por ServiceAccount y `audit` encadena discovery + permissions + scoring para producir hallazgos priorizados en `table`, `json` o `markdown`.

### 4.2 Componentes
- `cli`: arbol Cobra, validacion de flags y renderizado de salida.
- `k8s`: construccion de clientes Kubernetes (typed, dynamic, discovery) y resolucion de contexto.
- `discovery`: enumeracion y clasificacion de NHIs (SA, secrets, TLS, cert-manager).
- `permissions`: cache RBAC, resolucion binding->role->rules y analisis de sobreprivilegio.
- `scoring`: motor de reglas deterministas (16 reglas), severidades, filtros y recomendaciones.
- `models`: tipos compartidos de permisos/resolucion.
- `reporter`: stub reservado para formatos adicionales futuros.
- `scripts`: provision y limpieza de fixtures de prueba en cluster real.

### 4.3 Decisiones de arquitectura
- Decision 1: Decision: binario unico CLI en Go con paquetes `internal`. Razon: simplifica distribucion, ejecucion y mantenimiento para auditorias on-demand. Alternativa descartada: separar discovery/permissions/scoring en microservicios.
- Decision 2: Decision: scoring totalmente determinista basado en reglas, sin AI/ML. Razon: reproducibilidad, auditabilidad y explicabilidad de resultados de seguridad. Alternativa descartada: scoring heuristico no determinista.
- Decision 3: Decision: uso de cliente dinamico para recursos cert-manager en vez de SDK tipado dedicado. Razon: evitar dependencia directa de cert-manager y mantener compatibilidad aunque la CRD no exista. Alternativa descartada: importar tipos/cliente de cert-manager.
- Decision 4: Decision: discovery paralelo con tolerancia a errores parciales por fase. Razon: mejorar tiempo total de escaneo y no abortar todo por un fallo puntual (ej. cert-manager ausente). Alternativa descartada: pipeline secuencial fail-fast.

### 4.4 Diagrama (opcional)
`CLI -> k8s clients -> discovery (SA + Secrets + cert-manager) -> permissions resolver/analyzer -> scoring engine -> renderer (table/json/markdown)`

## 5. Estado Actual
### Completado
- Comando `discover` funcional con filtros por namespace, tipo y stale.
- Comando `permissions` funcional con detalle por SA, scope y flags de sobreprivilegio.
- Comando `audit` funcional integrando discovery + RBAC + scoring.
- Motor de scoring fase 3 implementado con 16 reglas y recomendaciones.
- Tests unitarios amplios para discovery, permissions y scoring; `go test ./...` pasa.
- CI activa con lint, test (Go 1.22/1.23) y build.

### En Curso
- No hay una feature en desarrollo activo dentro del repo; el siguiente bloque declarado en README es Fase 4 (analisis de uso real de permisos por audit logs).
- Preparacion de soporte configurable de reglas (`--rules-config`) existe como hook pero aun devuelve error.

### Pendiente
- P0: Fase 4 - analisis de gap entre permisos otorgados y uso real (audit logs).
- P1: Implementar carga real de reglas desde `configs/scoring-rules.yaml` para `audit --rules-config`.
- P1: Fase 5 - remediacion asistida (generacion de RBAC minimo y dry-run).
- P2: Fase 6 - extensiones OpenShift, release packaging (Goreleaser) y Helm chart.
- P2: Implementar `internal/reporter` (hoy es stub).

### Estimacion global: 72% completado

## 6. Lo que NO Hacer
- NO: leer o exponer valores de Secrets durante discovery; el proyecto solo inspecciona metadatos y nombres de keys.
- NO: introducir reglas de scoring no deterministas o dependientes de AI para decisiones de riesgo base.
- NO: romper IDs/reglas/severidades sin actualizar tests y compatibilidad de salida (consumidores pueden depender de esos campos).
- NO: convertir discovery en flujo secuencial fail-fast; se perderia resiliencia y rendimiento actual.
- NUNCA: modificar comportamiento RBAC critico (scope/flags) sin pruebas unitarias nuevas en `internal/permissions`.

## 7. Instrucciones para Codex
### Como arrancar el proyecto
```bash
git clone https://github.com/Zyrakk/nhi-watch.git
cd nhi-watch
go mod tidy
make build
./bin/nhi-watch version
```

### Como ejecutar tests
```bash
go test ./...
make test
```

### Rama base
Usar `main` como base. Crear `feature/{ticket}-{slug}` por tarea y rebasear antes de abrir PR.

### Ficheros que NO tocar
- `bin/nhi-watch`: artefacto compilado; no editar manualmente.
- `go.sum`: no editar a mano salvo cambios legitimos de dependencias via `go mod`.
- `configs/scoring-rules.yaml`: mantener sincronizado con implementacion si se activa `--rules-config`; evitar cambios cosmeticos sin soporte en codigo.

### Formato esperado del output
- Cambios de codigo acotados al alcance de la tarea.
- Tests nuevos o ajustados cuando se cambie logica.
- Resumen tecnico corto de cambios y validacion ejecutada.
- Commit message preferente en estilo conventional commits (`feat:`, `fix:`, `refactor:`, etc.).

### Cache de sesion
El worker debe mantener un fichero `sessions/cache/{session-id}.md` con:
- Decisiones tomadas y por que.
- Problemas encontrados y soluciones aplicadas.
- Ficheros modificados.
- Estado del trabajo si se pausa.

## 8. Historial de Sesiones
- Sesion 2026-02-08 (bootstrap + CI inicial)
- Que funciono: estructura base `cmd/` + `internal/` quedo lista rapido y permitio iterar por fases.
- Que no funciono: pipeline CI requirio varios fixes tempranos (`ci version`, `ci name`, linter/gosimple/formato).
- Leccion: cerrar higiene de CI/lint al inicio evita arrastre de deuda en fases funcionales.

- Sesion 2026-02-08 (v0.1.0 - discovery completo)
- Que funciono: discovery de SA, Secrets, TLS y cert-manager quedo operativo con filtros y tests.
- Que no funciono: aparecieron ajustes posteriores de scripts/formatos para estabilizar validaciones locales.
- Leccion: para features de discovery, incluir scripts reproducibles de fixtures reduce incertidumbre en pruebas manuales.

- Sesion 2026-02-09 (v0.2.0 - RBAC permissions)
- Que funciono: resolucion completa SA -> bindings -> roles -> rules y analisis de flags/scope con cobertura de tests alta.
- Que no funciono: edge-cases de bindings huerfanos y sujetos por grupo exigieron tests dedicados para evitar falsos positivos.
- Leccion: centralizar cache RBAC y resolver todo en memoria mejora rendimiento y consistencia del analisis.

- Sesion 2026-02-09 (v0.3.0 - scoring + audit unificado)
- Que funciono: motor determinista de 16 reglas y comando `audit` integrado (discovery + permissions + scoring).
- Que no funciono: `--rules-config` quedo expuesto pero no implementado, generando UX incompleta.
- Leccion: no exponer flags de roadmap en CLI productiva sin soporte funcional minimo.

- Sesion 2026-02-09 (hardening final de fase 3)
- Que funciono: ajustes de linter y README dejaron build/test/documentacion en estado estable.
- Que no funciono: hubo retrabajo documental de ultima hora para alinear ejemplos y estado real.
- Leccion: tratar README como artefacto versionado de release, no como tarea de cierre tardia.

---
- Errores pasados que quieres evitar: cambios en reglas/flags sin actualizar tests rompen consistencia de auditoria.
- Que esta en curso ahora mismo: definir e implementar Fase 4 (usage analysis) y activacion real de reglas configurables.
- Convenciones no escritas que sigues: priorizar salida determinista, mensajes de error con contexto y compatibilidad Kubernetes/k3s/OpenShift sin dependencias pesadas extra.
