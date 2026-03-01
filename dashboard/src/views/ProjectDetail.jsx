import { useEffect, useMemo, useState } from 'react';
import { Link, NavLink, useNavigate, useParams } from 'react-router-dom';
import AlertBanner from '../components/AlertBanner';
import MilestoneRoadmap from '../components/MilestoneRoadmap';
import ProgressBar from '../components/ProgressBar';
import TaskList from '../components/TaskList';
import Timeline from '../components/Timeline';
import { getMockProjectDetail } from '../mockData';

const POLL_INTERVAL_MS = 30000;

const projectStatusStyles = {
  working: 'text-hivemind-green bg-hivemind-green/10 border-hivemind-green/30',
  needs_input: 'text-hivemind-yellow bg-hivemind-yellow/10 border-hivemind-yellow/30',
  pending_review: 'text-hivemind-blue bg-hivemind-blue/10 border-hivemind-blue/30',
  blocked: 'text-hivemind-red bg-hivemind-red/10 border-hivemind-red/30',
  paused: 'text-hivemind-gray bg-hivemind-gray/10 border-hivemind-gray/30'
};

function formatStatusLabel(status) {
  return String(status || 'unknown')
    .replaceAll('_', ' ')
    .replace(/\b\w/g, (match) => match.toUpperCase());
}

function normalizeProjectDetail(payload, fallbackID) {
  const base = payload && typeof payload === 'object' ? payload : {};

  const project = base.project ?? {
    id: fallbackID,
    name: fallbackID,
    status: 'paused'
  };

  const tasks = Array.isArray(base.tasks) ? base.tasks : [];
  const workers = Array.isArray(base.workers) ? base.workers : [];
  const recentEvents = Array.isArray(base.recent_events) ? base.recent_events : [];
  const progress = base.progress ?? {};

  return {
    project,
    tasks,
    workers,
    recent_events: recentEvents,
    progress: {
      overall: typeof progress.overall === 'number' ? progress.overall : 0,
      workstreams: Array.isArray(progress.workstreams) ? progress.workstreams : []
    }
  };
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

function formatETA(date) {
  return new Intl.DateTimeFormat('es-ES', {
    day: '2-digit',
    month: 'short'
  }).format(date);
}

function buildMilestones(detail) {
  const workstreams = detail.progress.workstreams;
  const now = new Date();

  if (workstreams.length > 0) {
    return workstreams.slice(0, 4).map((stream, index) => {
      const p = typeof stream.progress === 'number' ? stream.progress : 0;
      let status = 'pending';
      if (p >= 0.95) {
        status = 'completed';
      } else if (p >= 0.3) {
        status = 'in_progress';
      }

      const eta = new Date(now.getTime() + (index + 1) * 2 * 24 * 60 * 60 * 1000);
      return {
        name: stream.name,
        status,
        eta: formatETA(eta)
      };
    });
  }

  return [
    {
      name: `Kickoff ${detail.project.name ?? ''}`.trim(),
      status: 'completed',
      eta: formatETA(now)
    },
    {
      name: 'Implementacion',
      status: 'in_progress',
      eta: formatETA(new Date(now.getTime() + 3 * 24 * 60 * 60 * 1000))
    },
    {
      name: 'Revision',
      status: 'pending',
      eta: formatETA(new Date(now.getTime() + 6 * 24 * 60 * 60 * 1000))
    }
  ];
}

async function fetchProjectDetail(apiBaseURL, projectID, signal) {
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

export default function ProjectDetail({ apiBaseURL }) {
  const { id } = useParams();
  const navigate = useNavigate();

  const [detail, setDetail] = useState(() => normalizeProjectDetail(getMockProjectDetail(id), id));
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
        const payload = await fetchProjectDetail(apiBaseURL, id, controller.signal);
        if (!mounted) {
          return;
        }

        setDetail(normalizeProjectDetail(payload, id));
        setConnectionError(false);
        setLastUpdated(new Date());
      } catch (_error) {
        if (!mounted) {
          return;
        }

        const mock = getMockProjectDetail(id);
        if (mock) {
          setDetail(normalizeProjectDetail(mock, id));
        }
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

  const milestones = useMemo(() => buildMilestones(detail), [detail]);
  const progressBars = useMemo(() => {
    const streams = detail.progress.workstreams;
    if (streams.length > 0) {
      return streams;
    }

    return [{ name: 'Overall', progress: detail.progress.overall }];
  }, [detail]);

  const projectStatusClass = projectStatusStyles[detail.project.status] ?? projectStatusStyles.paused;

  return (
    <div className="min-h-screen bg-hivemind-bg text-hivemind-text">
      <header className="sticky top-0 z-20 border-b border-slate-700 bg-hivemind-bg/95 backdrop-blur">
        <div className="mx-auto flex w-full max-w-7xl flex-col gap-3 px-4 py-4 sm:px-6 lg:px-8">
          <div className="flex items-center justify-between gap-3">
            <button
              type="button"
              onClick={() => navigate('/')}
              className="rounded-md border border-slate-600 px-3 py-1.5 text-sm text-hivemind-muted transition hover:border-slate-500 hover:text-hivemind-text"
            >
              {'<'} Volver
            </button>
            <p className="text-xs text-hivemind-muted">
              Ultima actualizacion: <span className="text-hivemind-text">{formatLastUpdated(lastUpdated)}</span>
            </p>
          </div>

          <div className="flex flex-wrap items-center justify-between gap-3">
            <h1 className="text-2xl font-black tracking-tight">{detail.project.name}</h1>
            <span className={`rounded-full border px-3 py-1 text-sm font-semibold ${projectStatusClass}`}>
              {formatStatusLabel(detail.project.status)}
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
            <p className="text-sm text-hivemind-muted">Cargando detalle del proyecto...</p>
          </section>
        ) : null}

        <MilestoneRoadmap milestones={milestones} />

        <section className="rounded-xl border border-slate-700 bg-hivemind-card p-4 shadow-panel">
          <div className="mb-4 flex items-center justify-between gap-3">
            <h2 className="text-lg font-bold text-hivemind-text">Progreso por Workstream</h2>
            <p className="text-sm text-hivemind-muted">
              Overall: <span className="font-semibold text-hivemind-text">{Math.round((detail.progress.overall ?? 0) * 100)}%</span>
            </p>
          </div>
          <div className="space-y-4">
            {progressBars.map((stream) => (
              <ProgressBar
                key={stream.name}
                label={stream.name}
                progress={stream.progress}
              />
            ))}
          </div>
        </section>

        <TaskList tasks={detail.tasks} workers={detail.workers} />

        <Timeline events={detail.recent_events} />
      </main>
    </div>
  );
}
