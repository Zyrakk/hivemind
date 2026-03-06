import { useEffect, useMemo, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import AlertBanner from '../components/AlertBanner';
import ArchitectureDecisions from '../components/ArchitectureDecisions';
import ContributeNow from '../components/ContributeNow';
import LastSession from '../components/LastSession';
import QuickLinks from '../components/QuickLinks';
import SystemFooter from '../components/SystemFooter';
import { getProjectStatus } from '../components/statusSystem';
import { getMockProjectDetail } from '../mockData';
import { ProjectCarbonHeader } from './ProjectDetail';

const POLL_INTERVAL_MS = 30000;

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
      description: item.description ?? item.reason ?? 'No detail available',
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
    task: source.task ?? source.title ?? 'Untitled session',
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

  const recentEvents = Array.isArray(source.recent_events)
    ? source.recent_events
    : Array.isArray(mockDetail?.recent_events)
      ? mockDetail.recent_events
      : [];

  return {
    project,
    recent_events: recentEvents,
    context: {
      summary:
        contextSource.summary ??
        contextFallback.summary ??
        'No executive summary available for this project.',
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

function MobileCollapsibleSection({ title, count, defaultOpen = false, children }) {
  return (
    <>
      <section className="md:hidden">
        <details open={defaultOpen} className="group border border-hivemind-border bg-hivemind-surface">
          <summary className="flex cursor-pointer list-none items-center gap-2 px-4 py-3 text-[11px] uppercase tracking-[0.1em] text-hivemind-muted transition-colors duration-150 hover:text-hivemind-text focus-visible:outline focus-visible:outline-2 focus-visible:outline-hivemind-blue focus-visible:outline-offset-0">
            <span className="text-hivemind-dim group-open:hidden">▸</span>
            <span className="hidden text-hivemind-dim group-open:inline">▾</span>
            <span>{title}</span>
            {typeof count === 'number' ? <span className="text-hivemind-dim">[{count}]</span> : null}
          </summary>
          <div className="border-t border-hivemind-border px-4 py-3">{children}</div>
        </details>
      </section>

      <section className="hidden bg-hivemind-surface px-[18px] py-[14px] md:block">
        <div className="flex items-center gap-1">
          <span className="text-[10px] uppercase tracking-[0.15em] text-hivemind-dim">{title}</span>
          {typeof count === 'number' ? (
            <span className="text-[10px] uppercase tracking-[0.1em] text-hivemind-dim">[{count}]</span>
          ) : null}
        </div>
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

  const projectStatus = useMemo(() => getProjectStatus(data.project.status), [data.project.status]);
  const latestEvent = data.recent_events[0] ?? null;

  return (
    <div className="flex min-h-screen flex-col bg-hivemind-bg text-[12px] text-hivemind-text">
      <ProjectCarbonHeader
        id={id}
        projectName={data.project.name}
        projectStatus={projectStatus}
        connectionError={connectionError}
        lastUpdated={lastUpdated}
        latestEvent={latestEvent}
        eventCount={data.recent_events.length}
        onBack={() => navigate(`/project/${id}`)}
      />

      <main className="mx-auto flex w-full max-w-[1280px] flex-1 min-h-0 flex-col gap-[2px] px-5 py-4">
        {connectionError ? <AlertBanner variant="error" message="No connection to the orchestrator" /> : null}

        {loading ? (
          <section className="bg-hivemind-surface px-[18px] py-[14px]">
            <p className="border border-dashed border-hivemind-border px-4 py-5 text-[11px] text-hivemind-dim">
              Loading context stream...
            </p>
          </section>
        ) : null}

        <section className="grid flex-1 min-h-0 gap-[2px] md:grid-cols-[minmax(0,1fr)_300px]">
          <div className="flex min-h-0 min-w-0 flex-col gap-[2px]">
            <section className="bg-hivemind-surface px-[18px] py-[14px]">
              <span className="text-[10px] uppercase tracking-[0.15em] text-hivemind-dim">BRIEFING</span>
              <p className="mt-3 text-[12px] leading-[1.6] text-hivemind-muted">{data.context.summary}</p>
            </section>

            <MobileCollapsibleSection
              title="ARCHITECTURE"
              count={data.context.architecture_decisions.length}
              defaultOpen
            >
              <ArchitectureDecisions decisions={data.context.architecture_decisions} />
            </MobileCollapsibleSection>

            <ContributeNow content={data.context.contribute_now} />
          </div>

          <div className="flex min-h-0 min-w-0 flex-col gap-[2px]">
            <MobileCollapsibleSection title="LINKS" defaultOpen>
              <QuickLinks quickLinks={data.context.quick_links} />
            </MobileCollapsibleSection>

            <MobileCollapsibleSection title="LAST SESSION" defaultOpen>
              <LastSession lastSession={data.context.last_session} />
            </MobileCollapsibleSection>
          </div>
        </section>
      </main>

      <SystemFooter />
    </div>
  );
}
