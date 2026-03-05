import { useEffect, useMemo, useState } from 'react';
import { NavLink, useNavigate, useParams } from 'react-router-dom';
import AlertBanner from '../components/AlertBanner';
import FlashTicker from '../components/FlashTicker';
import MilestoneRoadmap from '../components/MilestoneRoadmap';
import ProgressBar from '../components/ProgressBar';
import SystemFooter from '../components/SystemFooter';
import TaskList from '../components/TaskList';
import Timeline from '../components/Timeline';
import { getProjectStatus } from '../components/statusSystem';
import { getMockProjectDetail } from '../mockData';

const POLL_INTERVAL_MS = 30000;

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

  return new Intl.DateTimeFormat('en-GB', {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit'
  }).format(dateValue);
}

function formatRelativeTime(dateValue) {
  if (!dateValue) {
    return '--';
  }

  const date = new Date(dateValue);
  if (Number.isNaN(date.getTime())) {
    return '--';
  }

  const diffMs = Date.now() - date.getTime();
  const diffMin = Math.max(0, Math.floor(diffMs / 60000));

  if (diffMin < 1) {
    return '<1m';
  }
  if (diffMin < 60) {
    return `${diffMin}m`;
  }

  const diffHours = Math.floor(diffMin / 60);
  if (diffHours < 24) {
    return `${diffHours}h`;
  }

  const diffDays = Math.floor(diffHours / 24);
  return `${diffDays}d`;
}

function formatETA(date) {
  return new Intl.DateTimeFormat('en-GB', {
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
      name: 'Implementation',
      status: 'in_progress',
      eta: formatETA(new Date(now.getTime() + 3 * 24 * 60 * 60 * 1000))
    },
    {
      name: 'Review',
      status: 'pending',
      eta: formatETA(new Date(now.getTime() + 6 * 24 * 60 * 60 * 1000))
    }
  ];
}

function withAlpha(hex, alpha) {
  const normalized = hex.replace('#', '');
  const r = Number.parseInt(normalized.slice(0, 2), 16);
  const g = Number.parseInt(normalized.slice(2, 4), 16);
  const b = Number.parseInt(normalized.slice(4, 6), 16);

  return `rgba(${r}, ${g}, ${b}, ${alpha})`;
}

function workerStatusSquare(status) {
  if (status === 'running') {
    return 'bg-hivemind-green';
  }
  if (status === 'failed' || status === 'blocked') {
    return 'bg-hivemind-red';
  }
  if (status === 'completed') {
    return 'bg-hivemind-blue';
  }
  return 'bg-hivemind-gray';
}

export function ProjectCarbonHeader({
  id,
  projectName,
  projectStatus,
  connectionError,
  lastUpdated,
  latestEvent,
  eventCount,
  onBack
}) {
  return (
    <header className="sticky top-0 z-30">
      <div className="border-b border-hivemind-border bg-[#0d0d0d]">
        <div className="mx-auto flex w-full max-w-[1200px] items-center justify-between px-4 py-1.5 text-[9px] sm:px-5">
          <div className="flex min-w-0 items-center gap-2 uppercase tracking-[0.12em]">
            <button
              type="button"
              onClick={onBack}
              className="cursor-pointer text-hivemind-muted transition-colors duration-150 hover:text-hivemind-text focus-visible:outline focus-visible:outline-2 focus-visible:outline-hivemind-blue focus-visible:outline-offset-0"
            >
              &lt; BACK
            </button>
            <span className="text-hivemind-dim">|</span>
            <span className="text-[11px] font-bold text-hivemind-text">HIVEMIND</span>
            <span className="text-hivemind-dim">|</span>
            <span className="text-hivemind-dim">k3s</span>
            <span className="text-hivemind-dim">|</span>
            <span className={connectionError ? 'text-hivemind-yellow' : 'text-hivemind-green'}>
              {connectionError ? 'WARN' : 'CONN'}
            </span>
          </div>

          <span className="tabular-nums text-hivemind-muted">{formatLastUpdated(lastUpdated)}</span>
        </div>
      </div>

      <FlashTicker event={latestEvent} eventCount={eventCount} />

      <div className="border-b border-hivemind-border bg-hivemind-surface">
        <div className="mx-auto flex w-full max-w-[1200px] items-center justify-between gap-2 px-4 py-2 sm:px-5">
          <div className="flex min-w-0 items-center gap-2">
            <h1 className="truncate text-[14px] font-bold text-hivemind-text">{projectName}</h1>
            <span
              className="shrink-0 px-2 py-[2px] text-[9px] font-semibold uppercase"
              style={{
                border: `1px solid ${withAlpha(projectStatus.hex, 0.3)}`,
                backgroundColor: withAlpha(projectStatus.hex, 0.08),
                color: projectStatus.hex
              }}
            >
              {projectStatus.label}
            </span>
          </div>

          <nav className="flex items-center gap-px">
            <NavLink
              to={`/project/${id}`}
              end
              className={({ isActive }) =>
                `cursor-pointer px-3 py-1 text-[9px] uppercase tracking-[0.08em] transition-colors duration-150 focus-visible:outline focus-visible:outline-2 focus-visible:outline-hivemind-blue focus-visible:outline-offset-0 ${
                  isActive
                    ? 'border border-transparent text-hivemind-text'
                    : 'border border-hivemind-border text-hivemind-muted hover:text-hivemind-text'
                }`
              }
              style={({ isActive }) =>
                isActive
                  ? {
                      backgroundColor: withAlpha(projectStatus.hex, 0.1)
                    }
                  : undefined
              }
            >
              PROGRESS
            </NavLink>
            <NavLink
              to={`/project/${id}/context`}
              className={({ isActive }) =>
                `cursor-pointer px-3 py-1 text-[9px] uppercase tracking-[0.08em] transition-colors duration-150 focus-visible:outline focus-visible:outline-2 focus-visible:outline-hivemind-blue focus-visible:outline-offset-0 ${
                  isActive
                    ? 'border border-transparent text-hivemind-text'
                    : 'border border-hivemind-border text-hivemind-muted hover:text-hivemind-text'
                }`
              }
              style={({ isActive }) =>
                isActive
                  ? {
                      backgroundColor: withAlpha(projectStatus.hex, 0.1)
                    }
                  : undefined
              }
            >
              CONTEXT
            </NavLink>
          </nav>
        </div>
      </div>
    </header>
  );
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

  const projectStatus = getProjectStatus(detail.project.status);
  const latestEvent = detail.recent_events[0] ?? null;

  return (
    <div className="flex min-h-screen flex-col bg-hivemind-bg text-hivemind-text">
      <ProjectCarbonHeader
        id={id}
        projectName={detail.project.name}
        projectStatus={projectStatus}
        connectionError={connectionError}
        lastUpdated={lastUpdated}
        latestEvent={latestEvent}
        eventCount={detail.recent_events.length}
        onBack={() => navigate('/')}
      />

      <main className="mx-auto flex w-full max-w-[1200px] flex-1 flex-col gap-px px-4 py-3 sm:px-5">
        {connectionError ? <AlertBanner variant="error" message="No connection to the orchestrator" /> : null}

        {loading ? (
          <section className="bg-hivemind-surface px-3 py-3">
            <p className="border border-dashed border-hivemind-border px-3 py-4 text-[9px] text-hivemind-dim">
              Loading project telemetry...
            </p>
          </section>
        ) : null}

        <section className="grid gap-px md:grid-cols-[minmax(0,1fr)_320px]">
          <div className="flex min-w-0 flex-col gap-px">
            <MilestoneRoadmap milestones={milestones} />

            <section className="bg-hivemind-surface px-3 py-2.5">
              <div className="flex items-center justify-between gap-2">
                <span className="text-[8px] uppercase tracking-[0.15em] text-hivemind-dim">PROGRESS</span>
                <span className="text-[10px] text-hivemind-muted">
                  Overall: <span className="tabular-nums">{Math.round((detail.progress.overall ?? 0) * 100)}%</span>
                </span>
              </div>

              <div className="mt-2 space-y-2">
                {progressBars.map((stream) => (
                  <ProgressBar key={stream.name} label={stream.name} progress={stream.progress} />
                ))}
              </div>
            </section>

            <TaskList tasks={detail.tasks} workers={detail.workers} />
          </div>

          <aside className="flex min-w-0 flex-col gap-px">
            <section className="bg-hivemind-surface px-3 py-2">
              <div className="flex items-center gap-1">
                <span className="text-[8px] uppercase tracking-[0.15em] text-hivemind-dim">WORKERS</span>
                <span className="text-[8px] uppercase tracking-[0.1em] text-hivemind-dim">[{detail.workers.length}]</span>
              </div>

              {detail.workers.length === 0 ? (
                <p className="mt-2 border border-dashed border-hivemind-border px-3 py-4 text-[9px] text-hivemind-dim">
                  No active workers
                </p>
              ) : (
                <div className="mt-1">
                  {detail.workers.map((worker) => (
                    <article key={worker.id} className="border-b border-hivemind-border py-2 last:border-b-0">
                      <div className="flex items-center justify-between gap-2">
                        <p className="min-w-0 truncate text-[10px] font-semibold text-hivemind-text">
                          {worker.project_name ?? worker.session_id ?? worker.project_id ?? `worker-${worker.id}`}
                        </p>
                        <p className="shrink-0 text-[9px] text-hivemind-dim tabular-nums">
                          {formatRelativeTime(worker.started_at)}
                        </p>
                      </div>

                      <p
                        className="mt-1 text-[9px] text-hivemind-muted"
                        style={{
                          overflow: 'hidden',
                          display: '-webkit-box',
                          WebkitLineClamp: 2,
                          WebkitBoxOrient: 'vertical'
                        }}
                        title={worker.task_description}
                      >
                        {worker.task_description ?? 'No task description'}
                      </p>

                      <div className="mt-1 flex items-center justify-between gap-2">
                        <p className="min-w-0 truncate text-[8px] text-hivemind-dim" title={worker.branch}>
                          {worker.branch ?? '--'}
                        </p>
                        <span className={`h-1 w-1 shrink-0 ${workerStatusSquare(worker.status)}`} />
                      </div>
                    </article>
                  ))}
                </div>
              )}
            </section>

            <Timeline events={detail.recent_events} />
          </aside>
        </section>
      </main>

      <SystemFooter />
    </div>
  );
}
