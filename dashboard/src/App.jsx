import { useEffect, useMemo, useState } from 'react';
import {
  BrowserRouter,
  Navigate,
  NavLink,
  Route,
  Routes,
  useNavigate
} from 'react-router-dom';
import AlertBanner from './components/AlertBanner';
import GlobalCounters from './components/GlobalCounters';
import ProjectCard from './components/ProjectCard';
import WorkerList from './components/WorkerList';
import { mockState } from './mockData';
import ProjectDetail from './views/ProjectDetail';
import ProjectContext from './views/ProjectContext';

const API_BASE_URL = import.meta.env.VITE_API_BASE_URL ?? 'http://localhost:8080';
const POLL_INTERVAL_MS = 15000;

function normalizeState(payload) {
  if (!payload || typeof payload !== 'object') {
    return mockState;
  }

  const projects = Array.isArray(payload.projects) ? payload.projects : [];
  const workers = Array.isArray(payload.active_workers) ? payload.active_workers : [];
  const counters = payload.counters ?? {};

  return {
    projects,
    active_workers: workers,
    counters: {
      active_workers:
        typeof counters.active_workers === 'number'
          ? counters.active_workers
          : workers.length,
      pending_tasks:
        typeof counters.pending_tasks === 'number'
          ? counters.pending_tasks
          : projects.reduce((acc, p) => acc + (p.pending_tasks ?? 0), 0),
      pending_reviews:
        typeof counters.pending_reviews === 'number'
          ? counters.pending_reviews
          : projects.filter((p) => p.status === 'pending_review').length
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

async function fetchState(signal) {
  const response = await fetch(`${API_BASE_URL}/api/state`, {
    method: 'GET',
    headers: { Accept: 'application/json' },
    signal
  });

  if (!response.ok) {
    throw new Error(`backend error ${response.status}`);
  }

  return response.json();
}

function DashboardOverview() {
  const navigate = useNavigate();

  const [dashboardState, setDashboardState] = useState(() => normalizeState(mockState));
  const [lastUpdated, setLastUpdated] = useState(() => new Date());
  const [connectionError, setConnectionError] = useState(false);

  useEffect(() => {
    let isMounted = true;
    let intervalID;

    const load = async () => {
      const controller = new AbortController();
      const timeoutID = setTimeout(() => controller.abort(), 8000);

      try {
        const payload = await fetchState(controller.signal);
        if (!isMounted) {
          return;
        }

        setDashboardState(normalizeState(payload));
        setLastUpdated(new Date());
        setConnectionError(false);
      } catch (_error) {
        if (!isMounted) {
          return;
        }

        setConnectionError(true);
      } finally {
        clearTimeout(timeoutID);
      }
    };

    load();
    intervalID = window.setInterval(load, POLL_INTERVAL_MS);

    return () => {
      isMounted = false;
      window.clearInterval(intervalID);
    };
  }, []);

  const needsInputProjects = useMemo(
    () => dashboardState.projects.filter((project) => project.status === 'needs_input'),
    [dashboardState.projects]
  );

  const handleNeedsInputClick = () => {
    const first = needsInputProjects[0];
    if (first) {
      navigate(`/project/${first.id}`);
    }
  };

  return (
    <div className="min-h-screen bg-hivemind-bg text-hivemind-text">
      <header className="sticky top-0 z-20 border-b border-slate-700 bg-hivemind-bg/95 backdrop-blur">
        <div className="mx-auto flex w-full max-w-7xl flex-col gap-4 px-4 py-4 sm:px-6 lg:px-8">
          <div className="flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
            <h1 className="text-2xl font-black tracking-tight">Hivemind Dashboard</h1>
            <p className="text-sm text-hivemind-muted">
              Ultima actualizacion: <span className="text-hivemind-text">{formatLastUpdated(lastUpdated)}</span>
            </p>
          </div>

          <nav className="flex flex-wrap items-center gap-2 text-sm">
            <NavLink
              className="rounded-md bg-hivemind-blue/20 px-3 py-1.5 font-semibold text-hivemind-blue"
              to="/"
            >
              Vista 1: Estado General
            </NavLink>
            <span className="rounded-md border border-slate-600 px-3 py-1.5 text-hivemind-muted">
              Vista 2: Progreso (por proyecto)
            </span>
            <span className="rounded-md border border-slate-600 px-3 py-1.5 text-hivemind-muted">
              Vista 3: Contexto (por proyecto)
            </span>
          </nav>
        </div>
      </header>

      <main className="mx-auto flex w-full max-w-7xl flex-col gap-5 px-4 py-6 sm:px-6 lg:px-8">
        {connectionError ? (
          <AlertBanner
            variant="error"
            message="Sin conexion con el orquestador"
          />
        ) : null}

        {needsInputProjects.length > 0 ? (
          <AlertBanner
            message={`${needsInputProjects.length} proyectos necesitan tu atencion`}
            actionLabel="Ir al primero"
            onAction={handleNeedsInputClick}
          />
        ) : null}

        <GlobalCounters counters={dashboardState.counters} />

        <section className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
          {dashboardState.projects.map((project) => (
            <ProjectCard key={project.id} project={project} />
          ))}
        </section>

        <WorkerList workers={dashboardState.active_workers} />
      </main>
    </div>
  );
}

export default function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route path="/" element={<DashboardOverview />} />
        <Route path="/project/:id" element={<ProjectDetail apiBaseURL={API_BASE_URL} />} />
        <Route path="/project/:id/context" element={<ProjectContext apiBaseURL={API_BASE_URL} />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </BrowserRouter>
  );
}
