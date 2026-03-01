import { useEffect, useMemo, useState } from 'react';
import { Link, NavLink, useNavigate, useParams } from 'react-router-dom';
import AlertBanner from '../components/AlertBanner';
import ArchitectureDecisions from '../components/ArchitectureDecisions';
import ContributeNow from '../components/ContributeNow';
import LastSession from '../components/LastSession';
import QuickLinks from '../components/QuickLinks';
import { getMockProjectDetail } from '../mockData';

const POLL_INTERVAL_MS = 30000;

const projectStatusStyles = {
  working: 'text-hivemind-green bg-hivemind-green/10 border-hivemind-green/30',
  needs_input: 'text-hivemind-yellow bg-hivemind-yellow/10 border-hivemind-yellow/30',
  pending_review: 'text-hivemind-blue bg-hivemind-blue/10 border-hivemind-blue/30',
  blocked: 'text-hivemind-red bg-hivemind-red/10 border-hivemind-red/30',
  paused: 'text-hivemind-gray bg-hivemind-gray/10 border-hivemind-gray/30'
};

// Expected backend payload extension for GET /api/project/{id}
// {
//   "context": {
//     "summary": "string",
//     "architecture_decisions": [
//       { "id": "string", "title": "string", "description": "string", "type": "database|api|structure|security" }
//     ],
//     "last_session": {
//       "date": "ISO-8601",
//       "task": "string",
//       "result": "success|partial|failed",
//       "did": ["string"],
//       "pending": ["string"]
//     },
//     "quick_links": {
//       "repository": "url",
//       "open_prs": "url",
//       "agents_md": "url",
//       "active_branch": { "name": "string", "url": "url" }
//     },
//     "contribute_now": ["string"]
//   }
// }

function formatStatusLabel(status) {
  return String(status || 'unknown')
    .replaceAll('_', ' ')
    .replace(/\b\w/g, (match) => match.toUpperCase());
}

function formatLastUpdated(dateValue) {
  if (!dateValue) {
    return '--:--:--';
  }

  return new Intl.DateTimeFormat('es-ES', {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit'
  }).format(dateValue);
}

function normalizeQuickLinks(quickLinks, fallback) {
  const source = quickLinks && typeof quickLinks === 'object' ? quickLinks : {};
  const fallbackSource = fallback && typeof fallback === 'object' ? fallback : {};

  const activeBranchObject =
    source.active_branch && typeof source.active_branch === 'object'
      ? source.active_branch
      : fallbackSource.active_branch && typeof fallbackSource.active_branch === 'object'
        ? fallbackSource.active_branch
        : null;

  return {
    repository:
      source.repository ?? source.repo_url ?? source.repo ?? fallbackSource.repository ?? null,
    open_prs:
      source.open_prs ?? source.prs ?? source.pull_requests ?? fallbackSource.open_prs ?? null,
    agents_md:
      source.agents_md ?? source.agents_url ?? source.agents_md_raw ?? fallbackSource.agents_md ?? null,
    active_branch:
      activeBranchObject ??
      (source.active_branch_url
        ? {
            name: source.active_branch_name ?? 'active',
            url: source.active_branch_url
          }
        : fallbackSource.active_branch ?? null)
  };
}

function normalizeArchitectureDecisions(decisions, fallback) {
  const list = Array.isArray(decisions) ? decisions : Array.isArray(fallback) ? fallback : [];

  return list.slice(0, 5).map((item, index) => {
    if (typeof item === 'string') {
      return {
        id: `decision-${index}`,
        title: `Decision ${index + 1}`,
        description: item,
        type: 'structure'
      };
    }

    return {
      id: item.id ?? `decision-${index}`,
      title: item.title ?? item.decision ?? `Decision ${index + 1}`,
      description: item.description ?? item.reason ?? 'Sin detalle',
      type: item.type ?? 'structure'
    };
  });
}

function normalizeLastSession(lastSession, fallback) {
  const source =
    lastSession && typeof lastSession === 'object'
      ? lastSession
      : fallback && typeof fallback === 'object'
        ? fallback
        : null;

  if (!source) {
    return null;
  }

  return {
    date: source.date ?? source.timestamp ?? null,
    task: source.task ?? source.title ?? 'Sesion sin titulo',
    result: source.result ?? source.status ?? 'partial',
    did: Array.isArray(source.did) ? source.did : Array.isArray(source.done) ? source.done : [],
    pending: Array.isArray(source.pending)
      ? source.pending
      : Array.isArray(source.remaining)
        ? source.remaining
        : []
  };
}

function normalizeContributeNow(value, fallback) {
  if (Array.isArray(value)) {
    return value;
  }
  if (typeof value === 'string') {
    return value
      .split('\n')
      .map((line) => line.replace(/^[-*]\s*/, '').trim())
      .filter(Boolean);
  }

  if (Array.isArray(fallback)) {
    return fallback;
  }
  if (typeof fallback === 'string') {
    return fallback
      .split('\n')
      .map((line) => line.replace(/^[-*]\s*/, '').trim())
      .filter(Boolean);
  }

  return [];
}

function normalizeProjectContext(payload, projectID, mockDetail) {
  const source = payload && typeof payload === 'object' ? payload : {};
  const project = source.project ?? mockDetail?.project ?? { id: projectID, name: projectID, status: 'paused' };

  const contextSource = source.context && typeof source.context === 'object' ? source.context : {};
  const contextFallback = mockDetail?.context && typeof mockDetail.context === 'object' ? mockDetail.context : {};

  return {
    project,
    context: {
      summary:
        contextSource.summary ??
        contextFallback.summary ??
        'Sin resumen ejecutivo disponible para este proyecto.',
      architecture_decisions: normalizeArchitectureDecisions(
        contextSource.architecture_decisions,
        contextFallback.architecture_decisions
      ),
      last_session: normalizeLastSession(contextSource.last_session, contextFallback.last_session),
      quick_links: normalizeQuickLinks(contextSource.quick_links, contextFallback.quick_links),
      contribute_now: normalizeContributeNow(
        contextSource.contribute_now,
        contextFallback.contribute_now
      )
    }
  };
}

function MobileCollapsibleSection({ title, children, defaultOpen = false }) {
  return (
    <>
      <section className="rounded-xl border border-slate-700 bg-hivemind-card p-4 shadow-panel md:hidden">
        <details open={defaultOpen}>
          <summary className="cursor-pointer text-base font-bold text-hivemind-text">{title}</summary>
          <div className="mt-3">{children}</div>
        </details>
      </section>

      <section className="hidden rounded-xl border border-slate-700 bg-hivemind-card p-4 shadow-panel md:block">
        <h2 className="text-lg font-bold text-hivemind-text">{title}</h2>
        <div className="mt-3">{children}</div>
      </section>
    </>
  );
}

async function fetchProjectContext(apiBaseURL, projectID, signal) {
  const response = await fetch(`${apiBaseURL}/api/project/${projectID}`, {
    method: 'GET',
    headers: { Accept: 'application/json' },
    signal
  });

  if (!response.ok) {
    throw new Error(`backend error ${response.status}`);
  }

  return response.json();
}

export default function ProjectContext({ apiBaseURL }) {
  const { id } = useParams();
  const navigate = useNavigate();

  const [data, setData] = useState(() => normalizeProjectContext(null, id, getMockProjectDetail(id)));
  const [loading, setLoading] = useState(true);
  const [connectionError, setConnectionError] = useState(false);
  const [lastUpdated, setLastUpdated] = useState(() => new Date());

  useEffect(() => {
    let mounted = true;
    let intervalID;

    const load = async () => {
      const controller = new AbortController();
      const timeoutID = setTimeout(() => controller.abort(), 10000);

      try {
        const payload = await fetchProjectContext(apiBaseURL, id, controller.signal);
        if (!mounted) {
          return;
        }

        const mock = getMockProjectDetail(id);
        setData(normalizeProjectContext(payload, id, mock));
        setConnectionError(false);
        setLastUpdated(new Date());
      } catch (_error) {
        if (!mounted) {
          return;
        }

        const mock = getMockProjectDetail(id);
        setData(normalizeProjectContext(null, id, mock));
        setConnectionError(true);
      } finally {
        if (mounted) {
          setLoading(false);
        }
        clearTimeout(timeoutID);
      }
    };

    setLoading(true);
    load();
    intervalID = window.setInterval(load, POLL_INTERVAL_MS);

    return () => {
      mounted = false;
      window.clearInterval(intervalID);
    };
  }, [apiBaseURL, id]);

  const projectStatusClass = useMemo(
    () => projectStatusStyles[data.project.status] ?? projectStatusStyles.paused,
    [data.project.status]
  );

  return (
    <div className="min-h-screen bg-hivemind-bg text-hivemind-text">
      <header className="sticky top-0 z-20 border-b border-slate-700 bg-hivemind-bg/95 backdrop-blur">
        <div className="mx-auto flex w-full max-w-7xl flex-col gap-3 px-4 py-4 sm:px-6 lg:px-8">
          <div className="flex items-center justify-between gap-3">
            <button
              type="button"
              onClick={() => navigate(`/project/${id}`)}
              className="rounded-md border border-slate-600 px-3 py-1.5 text-sm text-hivemind-muted transition hover:border-slate-500 hover:text-hivemind-text"
            >
              {'<'} Volver
            </button>
            <p className="text-xs text-hivemind-muted">
              Ultima actualizacion: <span className="text-hivemind-text">{formatLastUpdated(lastUpdated)}</span>
            </p>
          </div>

          <div className="flex flex-wrap items-center justify-between gap-3">
            <h1 className="text-2xl font-black tracking-tight">{data.project.name}</h1>
            <span className={`rounded-full border px-3 py-1 text-sm font-semibold ${projectStatusClass}`}>
              {formatStatusLabel(data.project.status)}
            </span>
          </div>

          <nav className="flex flex-wrap items-center gap-2 text-sm">
            <Link
              className="rounded-md border border-slate-600 px-3 py-1.5 text-hivemind-muted transition hover:border-slate-500 hover:text-hivemind-text"
              to="/"
            >
              Dashboard
            </Link>
            <NavLink
              className={({ isActive }) =>
                `rounded-md px-3 py-1.5 font-medium transition ${
                  isActive
                    ? 'bg-hivemind-blue/20 text-hivemind-blue'
                    : 'border border-slate-600 text-hivemind-muted hover:border-slate-500 hover:text-hivemind-text'
                }`
              }
              to={`/project/${id}`}
              end
            >
              Progreso
            </NavLink>
            <NavLink
              className={({ isActive }) =>
                `rounded-md px-3 py-1.5 font-medium transition ${
                  isActive
                    ? 'bg-hivemind-blue/20 text-hivemind-blue'
                    : 'border border-slate-600 text-hivemind-muted hover:border-slate-500 hover:text-hivemind-text'
                }`
              }
              to={`/project/${id}/context`}
            >
              Contexto
            </NavLink>
          </nav>
        </div>
      </header>

      <main className="mx-auto flex w-full max-w-7xl flex-col gap-5 px-4 py-6 sm:px-6 lg:px-8">
        {connectionError ? <AlertBanner variant="error" message="Sin conexion con el orquestador" /> : null}

        {loading ? (
          <section className="rounded-xl border border-slate-700 bg-hivemind-card p-6 shadow-panel">
            <p className="text-sm text-hivemind-muted">Cargando contexto del proyecto...</p>
          </section>
        ) : null}

        <section className="rounded-xl border border-slate-700 bg-slate-800/80 p-5 shadow-panel">
          <h2 className="text-xl font-bold text-hivemind-text">Resumen Ejecutivo</h2>
          <p className="mt-3 text-sm leading-relaxed text-hivemind-muted">{data.context.summary}</p>
        </section>

        <MobileCollapsibleSection title="Decisiones de Arquitectura" defaultOpen>
          <ArchitectureDecisions decisions={data.context.architecture_decisions} />
        </MobileCollapsibleSection>

        <MobileCollapsibleSection title="Ultima Sesion de Trabajo">
          <LastSession lastSession={data.context.last_session} />
        </MobileCollapsibleSection>

        <MobileCollapsibleSection title="Quick Links">
          <QuickLinks quickLinks={data.context.quick_links} />
        </MobileCollapsibleSection>

        <ContributeNow content={data.context.contribute_now} />
      </main>
    </div>
  );
}
