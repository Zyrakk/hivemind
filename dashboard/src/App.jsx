import { useEffect, useMemo, useRef, useState } from 'react';
import { BrowserRouter, Navigate, Route, Routes, useNavigate } from 'react-router-dom';
import AlertBanner from './components/AlertBanner';
import FlashTicker from './components/FlashTicker';
import GlobalCounters from './components/GlobalCounters';
import ProjectCard from './components/ProjectCard';
import StatusBar from './components/StatusBar';
import SystemFooter from './components/SystemFooter';
import WorkerList from './components/WorkerList';
import { mockState } from './mockData';
import ProjectContext from './views/ProjectContext';
import ProjectDetail from './views/ProjectDetail';

const API_BASE_URL = import.meta.env.VITE_API_BASE_URL ?? '';
const POLL_INTERVAL_MS = 15000;

function normalizeState(payload) {
  if (!payload || typeof payload !== 'object') {
    return normalizeState(mockState);
  }

  const projects = Array.isArray(payload.projects) ? payload.projects : [];
  const workers = Array.isArray(payload.active_workers) ? payload.active_workers : [];
  const counters = payload.counters ?? {};
  const recentEvents = Array.isArray(payload.recent_events) ? payload.recent_events : [];

  return {
    projects,
    active_workers: workers,
    recent_events: recentEvents,
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

  return new Intl.DateTimeFormat('en-GB', {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit'
  }).format(dateValue);
}

function newestEvent(events) {
  if (!Array.isArray(events) || events.length === 0) {
    return null;
  }

  return [...events].sort(
    (a, b) => new Date(b.timestamp ?? 0).getTime() - new Date(a.timestamp ?? 0).getTime()
  )[0];
}

function statusLabel(status) {
  const map = {
    working: 'ACT',
    needs_input: 'HLD',
    pending_review: 'REV',
    blocked: 'BLK',
    paused: 'OFF'
  };

  return map[status] ?? 'OFF';
}

function buildSyntheticEvent(previousState, currentState) {
  if (!previousState) {
    return null;
  }

  const previousWorkers = Array.isArray(previousState.active_workers) ? previousState.active_workers : [];
  const currentWorkers = Array.isArray(currentState.active_workers) ? currentState.active_workers : [];

  const previousWorkerIDs = new Set(previousWorkers.map((worker) => String(worker.id)));
  const currentWorkerIDs = new Set(currentWorkers.map((worker) => String(worker.id)));

  const newWorkers = currentWorkers.filter((worker) => !previousWorkerIDs.has(String(worker.id)));
  if (newWorkers.length > 0) {
    const worker = newWorkers[0];
    return {
      timestamp: new Date().toISOString(),
      description: `worker ${worker.session_id ?? worker.id} started`,
      event_type: 'worker_started'
    };
  }

  const previousProjects = Array.isArray(previousState.projects) ? previousState.projects : [];
  const currentProjects = Array.isArray(currentState.projects) ? currentState.projects : [];
  const previousByID = new Map(previousProjects.map((project) => [project.id, project]));

  for (const project of currentProjects) {
    const previous = previousByID.get(project.id);
    if (previous && previous.status !== project.status) {
      return {
        timestamp: new Date().toISOString(),
        description: `${project.name ?? project.id} status changed to ${statusLabel(project.status)}`,
        event_type: project.status === 'needs_input' ? 'input_needed' : 'task_completed'
      };
    }
  }

  if (currentWorkers.length < previousWorkers.length) {
    const endedWorkers = previousWorkers.filter((worker) => !currentWorkerIDs.has(String(worker.id)));
    const worker = endedWorkers[0];
    return {
      timestamp: new Date().toISOString(),
      description: `worker ${worker?.session_id ?? worker?.id ?? 'session'} completed`,
      event_type: 'task_completed'
    };
  }

  return null;
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
  const [latestEvent, setLatestEvent] = useState(() => newestEvent(mockState.recent_events ?? []) ?? null);
  const [eventCount, setEventCount] = useState(() =>
    Array.isArray(mockState.recent_events) ? mockState.recent_events.length : 0
  );

  const previousStateRef = useRef(normalizeState(mockState));
  const eventCountRef = useRef(Array.isArray(mockState.recent_events) ? mockState.recent_events.length : 0);

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

        const nextState = normalizeState(payload);
        setDashboardState(nextState);
        setLastUpdated(new Date());
        setConnectionError(false);

        if (nextState.recent_events.length > 0) {
          const latest = newestEvent(nextState.recent_events);
          setLatestEvent(latest);
          setEventCount(nextState.recent_events.length);
          eventCountRef.current = nextState.recent_events.length;
        } else {
          const syntheticEvent = buildSyntheticEvent(previousStateRef.current, nextState);
          if (syntheticEvent) {
            setLatestEvent(syntheticEvent);
            eventCountRef.current += 1;
            setEventCount(eventCountRef.current);
          }
        }

        previousStateRef.current = nextState;
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
    <div className="flex min-h-screen flex-col bg-hivemind-bg text-[12px] text-hivemind-text">
      <header className="sticky top-0 z-30 border-b border-hivemind-border bg-[#0d0d0d]">
        <div className="mx-auto flex w-full max-w-[1280px] items-center justify-between px-5 py-2 text-[11px]">
          <div className="flex min-w-0 items-center gap-2 uppercase tracking-[0.12em]">
            <span className="truncate font-bold text-hivemind-text">HIVEMIND</span>
            <span className="text-hivemind-dim">|</span>
            <span className="text-hivemind-dim">k3s</span>
            <span className="text-hivemind-dim">|</span>
            <span className={connectionError ? 'text-hivemind-yellow' : 'text-hivemind-green'}>
              {connectionError ? 'WARN' : 'CONN'}
            </span>
          </div>

          <div className="flex items-center gap-3 uppercase tracking-[0.1em]">
            <span className="text-hivemind-dim">{eventCount} events</span>
            <span className="text-hivemind-muted">{formatLastUpdated(lastUpdated)}</span>
          </div>
        </div>
      </header>

      <FlashTicker event={latestEvent} eventCount={eventCount} />

      <main className="mx-auto flex w-full max-w-[1280px] flex-1 min-h-0 flex-col gap-[2px] px-5 py-4">
        {connectionError ? (
          <AlertBanner variant="error" message="No connection to the orchestrator" />
        ) : null}

        {needsInputProjects.length > 0 ? (
          <AlertBanner
            message={`${needsInputProjects.length} unit${needsInputProjects.length > 1 ? 's' : ''} require operator input`}
            actionLabel="OPEN FIRST"
            onAction={handleNeedsInputClick}
          />
        ) : null}

        <section className="grid flex-1 min-h-0 gap-[2px] bg-hivemind-border md:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_300px] md:grid-rows-[auto_1fr]">
          <div className="md:col-span-3">
            <GlobalCounters counters={dashboardState.counters} />
          </div>

          <section className="min-h-0 bg-hivemind-surface px-[18px] py-[14px]">
            <p className="mb-[10px] text-[10px] uppercase tracking-[0.15em] text-hivemind-dim">PROJECTS</p>
            <StatusBar projects={dashboardState.projects} />

            <div className="overflow-x-auto">
              <table className="min-w-full text-left">
                <thead>
                  <tr className="border-b border-hivemind-border text-[10px] uppercase tracking-[0.1em] text-hivemind-dim">
                    <th className="px-1.5 py-2">NAME</th>
                    <th className="px-1.5 py-2">ST</th>
                    <th className="px-1.5 py-2">W</th>
                    <th className="px-1.5 py-2">T</th>
                    <th className="px-1.5 py-2">AGE</th>
                  </tr>
                </thead>
                <tbody>
                  {dashboardState.projects.map((project) => (
                    <ProjectCard key={project.id} project={project} />
                  ))}
                </tbody>
              </table>
            </div>
          </section>

          <WorkerList workers={dashboardState.active_workers} />

          <aside className="flex min-h-0 flex-col gap-[2px] bg-hivemind-border">
            <section className="bg-hivemind-surface px-[18px] py-[14px]">
              <p className="mb-[10px] text-[10px] uppercase tracking-[0.15em] text-hivemind-dim">SYSTEM</p>

              <div className="hidden text-[11px] md:block">
                <div className="grid grid-cols-[auto_1fr] gap-x-3">
                  <span className="py-[5px] text-hivemind-dim">cluster</span>
                  <span className="py-[5px] text-hivemind-muted">k3s</span>
                  <span className="py-[5px] text-hivemind-dim">store</span>
                  <span className="py-[5px] text-hivemind-muted">sqlite</span>
                  <span className="py-[5px] text-hivemind-dim">tunnel</span>
                  <span className="py-[5px] text-hivemind-muted">cloudflare</span>
                  <span className="py-[5px] text-hivemind-dim">poll</span>
                  <span className="py-[5px] text-hivemind-muted">15s</span>
                  <span className="py-[5px] text-hivemind-dim">ver</span>
                  <span className="py-[5px] text-hivemind-muted">0.3.0</span>
                </div>
              </div>

              <div className="flex flex-wrap items-center gap-x-3 gap-y-2 text-[11px] md:hidden">
                <span className="text-hivemind-dim">cluster:k3s</span>
                <span className="text-hivemind-dim">store:sqlite</span>
                <span className="text-hivemind-dim">tunnel:cloudflare</span>
                <span className="text-hivemind-dim">poll:15s</span>
                <span className="text-hivemind-dim">ver:0.3.0</span>
              </div>
            </section>

            <section className="flex flex-1 flex-col bg-hivemind-surface px-[18px] py-[14px]">
              <p className="mb-[10px] text-[10px] uppercase tracking-[0.15em] text-hivemind-dim">ENDPOINT</p>
              <p className="break-words text-[11px] text-hivemind-muted">hivemind.zyrak.cloud</p>
            </section>
          </aside>
        </section>
      </main>

      <SystemFooter
        units={dashboardState.projects.length}
        workers={dashboardState.active_workers.length}
      />
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
