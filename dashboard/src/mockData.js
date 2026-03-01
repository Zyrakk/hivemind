export const mockState = {
  projects: [
    {
      id: 'flux',
      name: 'Flux',
      status: 'working',
      active_workers: 2,
      pending_tasks: 3,
      last_activity: '2026-02-28T19:52:00Z'
    },
    {
      id: 'nhi-watch',
      name: 'NHI-Watch',
      status: 'needs_input',
      active_workers: 1,
      pending_tasks: 4,
      last_activity: '2026-02-28T19:42:00Z'
    },
    {
      id: 'zcloud',
      name: 'ZCloud',
      status: 'pending_review',
      active_workers: 0,
      pending_tasks: 2,
      last_activity: '2026-02-28T18:10:00Z'
    },
    {
      id: 'vuln-reporter',
      name: 'vuln-reporter',
      status: 'blocked',
      active_workers: 0,
      pending_tasks: 5,
      last_activity: '2026-02-28T17:02:00Z'
    }
  ],
  active_workers: [
    {
      id: 101,
      project_id: 'flux',
      project_name: 'Flux',
      session_id: 'flux-economy-001',
      task_description: 'Implementar endpoints para seccion economia y ajustar serializacion JSON',
      branch: 'feature/flux-economy',
      status: 'running',
      started_at: '2026-02-28T19:50:00Z'
    },
    {
      id: 102,
      project_id: 'flux',
      project_name: 'Flux',
      session_id: 'flux-feed-002',
      task_description: 'Refactor de workers RSS para deduplicacion incremental',
      branch: 'feature/flux-feed-refactor',
      status: 'running',
      started_at: '2026-02-28T19:46:00Z'
    },
    {
      id: 103,
      project_id: 'nhi-watch',
      project_name: 'NHI-Watch',
      session_id: 'nhi-review-009',
      task_description: 'Crear PR de consolidacion para vista de contexto rapido',
      branch: 'feature/nhi-context-view',
      status: 'running',
      started_at: '2026-02-28T19:40:00Z'
    }
  ],
  counters: {
    active_workers: 3,
    pending_tasks: 14,
    pending_reviews: 1
  }
};

export const mockProjectDetails = {
  flux: {
    project: {
      id: 'flux',
      name: 'Flux',
      description: 'Orquestador de contenido y pipeline editorial',
      status: 'working'
    },
    tasks: [
      {
        id: 1001,
        title: 'Endpoints economia',
        description: 'Implementar API para seccion economia con validaciones y contratos estables.',
        status: 'in_progress',
        assigned_worker_id: 101,
        depends_on: []
      },
      {
        id: 1002,
        title: 'Refactor feed RSS',
        description: 'Reducir duplicados y mejorar latencia de ingestion.',
        status: 'pending',
        assigned_worker_id: 102,
        depends_on: ['1001']
      },
      {
        id: 1003,
        title: 'QA de integracion',
        description: 'Correr suite de regresion despues del refactor.',
        status: 'blocked',
        assigned_worker_id: null,
        depends_on: ['1002']
      }
    ],
    workers: [
      {
        id: 101,
        session_id: 'flux-economy-001',
        status: 'running'
      },
      {
        id: 102,
        session_id: 'flux-feed-002',
        status: 'running'
      }
    ],
    recent_events: [
      {
        id: 1,
        event_type: 'worker_started',
        description: 'Worker flux-economy-001 iniciado',
        timestamp: '2026-02-28T19:50:00Z'
      },
      {
        id: 2,
        event_type: 'pr_created',
        description: 'PR preliminar de endpoints economia creado',
        timestamp: '2026-02-28T19:36:00Z'
      },
      {
        id: 3,
        event_type: 'input_needed',
        description: 'Definir criterio final para prioridad de noticias',
        timestamp: '2026-02-28T19:20:00Z'
      }
    ],
    progress: {
      overall: 0.65,
      workstreams: [
        { name: 'Seccion Economia', progress: 0.8 },
        { name: 'Feed RSS', progress: 0.45 },
        { name: 'Refactor Modulos', progress: 0.7 }
      ]
    },
    context: {
      summary:
        'Flux es el proyecto editorial principal de Hivemind. Coordina pipelines para generar contenido, revisar cambios y publicar resultados con trazabilidad. El estado actual es working con dos workers activos y un bloque QA pendiente. El foco inmediato es cerrar endpoints de economia y destrabar validaciones finales.',
      architecture_decisions: [
        {
          id: 'flux-1',
          title: 'Binario unico en Go',
          description: 'Decision: mantener un binario unico para planner, launcher y dashboard. Razon: simplificar operacion y despliegue en k3s.',
          type: 'structure'
        },
        {
          id: 'flux-2',
          title: 'SQLite como estado inicial',
          description: 'Decision: usar SQLite para estado operativo de workers y tareas. Razon: reducir dependencia externa y acelerar bootstrap.',
          type: 'database'
        },
        {
          id: 'flux-3',
          title: 'API REST con net/http',
          description: 'Decision: mantener net/http estandar sin framework. Razon: menor complejidad y control directo del contrato JSON.',
          type: 'api'
        }
      ],
      last_session: {
        date: '2026-02-28T19:50:00Z',
        task: 'Implementar vista de progreso y endpoints de economia',
        result: 'success',
        did: [
          'Se publico la vista de progreso por proyecto',
          'Se agregaron filtros de tareas y timeline de eventos',
          'Se validaron rutas /project/:id y polling de 30s'
        ],
        pending: [
          'Integrar campo context en backend para vista 3',
          'Completar cobertura de tests e2e del frontend'
        ]
      },
      quick_links: {
        repository: 'https://github.com/zyrak/hivemind',
        open_prs: 'https://github.com/zyrak/hivemind/pulls',
        agents_md: 'https://raw.githubusercontent.com/zyrak/hivemind/main/AGENTS.md',
        active_branch: {
          name: 'feature/flux-economy',
          url: 'https://github.com/zyrak/hivemind/tree/feature/flux-economy'
        }
      },
      contribute_now: [
        'El stack principal es Go 1.22 + SQLite + net/http, evita introducir frameworks nuevos.',
        'La linea activa es cerrar endpoints de economia y su validacion de contratos JSON.',
        'Las ramas activas son feature/flux-economy y feature/flux-feed-refactor.',
        'No modificar schema sin migracion versionada en internal/state/migrations.go.',
        'No introducir secretos ni tocar deploy/secrets.yaml.',
        'Priorizar cambios pequenos con tests locales reproducibles.'
      ]
    }
  },
  'nhi-watch': {
    project: {
      id: 'nhi-watch',
      name: 'NHI-Watch',
      description: 'Monitor de incidentes y alertas',
      status: 'needs_input'
    },
    tasks: [
      {
        id: 2001,
        title: 'Consolidar criterios de alerta',
        description: 'Esperando decision sobre umbrales por categoria.',
        status: 'blocked',
        assigned_worker_id: 103,
        depends_on: ['decision-alert-thresholds']
      }
    ],
    workers: [
      {
        id: 103,
        session_id: 'nhi-review-009',
        status: 'running'
      }
    ],
    recent_events: [
      {
        id: 4,
        event_type: 'input_needed',
        description: 'Se requiere aprobacion de umbrales para notificaciones',
        timestamp: '2026-02-28T19:40:00Z'
      }
    ],
    progress: {
      overall: 0.35,
      workstreams: [
        { name: 'Alerting Core', progress: 0.35 },
        { name: 'UX Review', progress: 0.2 }
      ]
    },
    context: {
      summary:
        'NHI-Watch monitorea incidentes y orquesta alertas. Esta en needs_input porque falta una decision de umbrales antes de continuar con automatizacion. El trabajo tecnico esta avanzado pero bloqueado en definiciones funcionales.',
      architecture_decisions: [
        {
          id: 'nhi-1',
          title: 'Reglas de alertado declarativas',
          description: 'Decision: definir umbrales en configuracion y no en codigo. Razon: facilitar cambios operativos sin redeploy.',
          type: 'structure'
        },
        {
          id: 'nhi-2',
          title: 'Eventos via API interna',
          description: 'Decision: exponer eventos via REST para dashboard y bot. Razon: observabilidad centralizada.',
          type: 'api'
        }
      ],
      last_session: {
        date: '2026-02-28T19:40:00Z',
        task: 'Revision de reglas de notificacion por severidad',
        result: 'partial',
        did: [
          'Se agruparon reglas por categoria de incidente',
          'Se detecto necesidad de definir umbral de escalado'
        ],
        pending: [
          'Aprobar umbrales por categoria',
          'Reactivar worker de validacion de alertas'
        ]
      },
      quick_links: {
        repository: 'https://github.com/zyrak/hivemind',
        open_prs: 'https://github.com/zyrak/hivemind/pulls?q=is%3Aopen+nhi-watch',
        agents_md: 'https://raw.githubusercontent.com/zyrak/hivemind/main/agents/nhi-watch.md',
        active_branch: {
          name: 'feature/nhi-context-view',
          url: 'https://github.com/zyrak/hivemind/tree/feature/nhi-context-view'
        }
      },
      contribute_now: [
        'Antes de tocar codigo, confirmar umbrales de alerta pendientes.',
        'El worker activo es nhi-review-009 y depende de esa decision.',
        'No mezclar cambios de UI con logica de reglas en la misma PR.',
        'Mantener trazabilidad en AGENTS.md y registrar decisiones clave.'
      ]
    }
  },
  zcloud: {
    project: {
      id: 'zcloud',
      name: 'ZCloud',
      description: 'Plataforma de despliegue K3s',
      status: 'pending_review'
    },
    tasks: [
      {
        id: 3001,
        title: 'Helm values cleanup',
        description: 'Listo para revision final.',
        status: 'completed',
        assigned_worker_id: null,
        depends_on: []
      }
    ],
    workers: [],
    recent_events: [
      {
        id: 5,
        event_type: 'task_completed',
        description: 'Cleanup de Helm completado',
        timestamp: '2026-02-28T18:10:00Z'
      },
      {
        id: 6,
        event_type: 'pr_created',
        description: 'PR de release candidate abierto',
        timestamp: '2026-02-28T18:00:00Z'
      }
    ],
    progress: {
      overall: 0.9,
      workstreams: [
        { name: 'Infra Upgrade', progress: 0.95 },
        { name: 'Observabilidad', progress: 0.8 }
      ]
    },
    context: {
      summary:
        'ZCloud concentra despliegue y operacion de k3s para el sistema. Esta en pending_review, sin workers activos, esperando aprobacion de PR final. El riesgo actual es bajo y queda cerrar revision de release.',
      architecture_decisions: [
        {
          id: 'zcloud-1',
          title: 'Helm como mecanismo de release',
          description: 'Decision: versionar despliegues con charts Helm. Razon: repetibilidad y rollback simple.',
          type: 'structure'
        },
        {
          id: 'zcloud-2',
          title: 'Secretos fuera del repo',
          description: 'Decision: no guardar secretos en Git. Razon: minimizar exposicion de credenciales.',
          type: 'security'
        }
      ],
      last_session: {
        date: '2026-02-28T18:10:00Z',
        task: 'Release candidate de chart y cleanup de values',
        result: 'success',
        did: [
          'Se unificaron values por entorno',
          'Se abrio PR de release candidate'
        ],
        pending: ['Aprobacion final y merge de PR']
      },
      quick_links: {
        repository: 'https://github.com/zyrak/hivemind',
        open_prs: 'https://github.com/zyrak/hivemind/pulls?q=is%3Aopen+zcloud',
        agents_md: 'https://raw.githubusercontent.com/zyrak/hivemind/main/agents/zcloud.md',
        active_branch: {
          name: 'feature/zcloud-release-candidate',
          url: 'https://github.com/zyrak/hivemind/tree/feature/zcloud-release-candidate'
        }
      },
      contribute_now: [
        'Prioridad: revisar y aprobar PR de release candidate.',
        'No tocar templates de Helm sin validar compatibilidad con cluster actual.',
        'Mantener secretos fuera de git y usar referencias externas.'
      ]
    }
  },
  'vuln-reporter': {
    project: {
      id: 'vuln-reporter',
      name: 'vuln-reporter',
      description: 'Agente de vulnerabilidades',
      status: 'blocked'
    },
    tasks: [
      {
        id: 4001,
        title: 'Integrar reporte CVE',
        description: 'Bloqueada por API key de proveedor externo.',
        status: 'blocked',
        assigned_worker_id: null,
        depends_on: ['external-api-key']
      }
    ],
    workers: [],
    recent_events: [
      {
        id: 7,
        event_type: 'worker_failed',
        description: 'Worker detenido por credenciales faltantes',
        timestamp: '2026-02-28T17:02:00Z'
      }
    ],
    progress: {
      overall: 0.2,
      workstreams: [
        { name: 'Scanner Core', progress: 0.25 },
        { name: 'CVE Feed', progress: 0.1 }
      ]
    },
    context: {
      summary:
        'vuln-reporter detecta riesgos de seguridad en cambios de codigo. Esta bloqueado por falta de credenciales externas para feed CVE. No hay workers activos y la prioridad es destrabar acceso seguro al proveedor.',
      architecture_decisions: [
        {
          id: 'vuln-1',
          title: 'Escaneo sin secretos en repositorio',
          description: 'Decision: inyectar credenciales por entorno, nunca en codigo. Razon: reducir superficie de riesgo.',
          type: 'security'
        },
        {
          id: 'vuln-2',
          title: 'Reporte estructurado por severidad',
          description: 'Decision: salida en JSON con severidad y evidencia. Razon: facilitar evaluacion automatica.',
          type: 'api'
        }
      ],
      last_session: null,
      quick_links: {
        repository: 'https://github.com/zyrak/hivemind',
        open_prs: 'https://github.com/zyrak/hivemind/pulls?q=is%3Aopen+vuln',
        agents_md: 'https://raw.githubusercontent.com/zyrak/hivemind/main/agents/vuln-reporter.md',
        active_branch: {
          name: 'feature/vuln-cve-feed',
          url: 'https://github.com/zyrak/hivemind/tree/feature/vuln-cve-feed'
        }
      },
      contribute_now: [
        'Antes de desarrollar, resolver provisioning seguro de API key CVE.',
        'Mantener regla: cero secretos en repo.',
        'Cuando se destrabe acceso, activar worker dedicado para smoke test de feed.',
        'No mezclar refactors no relacionados en la misma rama.'
      ]
    }
  }
};

export function getMockProjectDetail(projectID) {
  return mockProjectDetails[projectID] ?? null;
}
