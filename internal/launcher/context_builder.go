package launcher

import (
	"fmt"
	"strings"
)

func BuildWorkerContext(agentsMD, taskDesc string, cache string, sessionID string) string {
	agents := strings.TrimSpace(agentsMD)
	if agents == "" {
		agents = "AGENTS.md no disponible"
	}

	task := strings.TrimSpace(taskDesc)
	if task == "" {
		task = "Tarea no especificada"
	}

	cacheContent := strings.TrimSpace(cache)
	if cacheContent == "" {
		cacheContent = "No hay cache previo"
	}

	return fmt.Sprintf(`---
# Contexto de Trabajo — Sesion %s

## Tu Rol
Eres un worker Codex ejecutando una tarea especifica. Trabaja SOLO en la tarea descrita abajo.
No tomes decisiones de arquitectura. Si encuentras ambiguedad, documenta la pregunta en el cache y continua con la opcion mas segura.

## Proyecto
%s

## Tarea Asignada
%s

## Cache de Sesiones Anteriores
%s

## Instrucciones Obligatorias
1. Trabaja SOLO en los ficheros listados en la tarea. No modifiques otros ficheros.
2. Sigue las convenciones del proyecto definidas en el AGENTS.md.
3. Escribe tests para todo codigo nuevo.
4. Haz commits atomicos con mensajes descriptivos (conventional commits).
5. Mantiene actualizado el fichero sessions/cache/%s.md con:
   - Decisiones tomadas y por que
   - Problemas encontrados
   - Estado del trabajo
6. Si no puedes completar la tarea, documenta que queda pendiente en el cache.
---
`, sessionID, agents, task, cacheContent, sessionID)
}
