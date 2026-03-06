import { useNavigate } from 'react-router-dom';
import { getProjectStatus } from './statusSystem';

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

export default function ProjectCard({ project }) {
  const navigate = useNavigate();
  const status = getProjectStatus(project.status);

  const workers = Number.isFinite(project.active_workers) ? project.active_workers : 0;
  const tasks = Number.isFinite(project.pending_tasks) ? project.pending_tasks : 0;

  const onOpen = () => navigate(`/project/${project.id}`);

  return (
    <tr
      className="cursor-pointer border-b border-hivemind-border/80 text-[12px] transition-colors duration-150 last:border-b-0 hover:bg-[#252525]"
      onClick={onOpen}
      onKeyDown={(event) => {
        if (event.key === 'Enter' || event.key === ' ') {
          event.preventDefault();
          onOpen();
        }
      }}
      tabIndex={0}
      role="button"
      aria-label={`Open project ${project.name}`}
    >
      <td className="px-1.5 py-2 text-[13px] font-semibold text-hivemind-text">{project.name}</td>
      <td className={`px-1.5 py-2 text-[11px] font-semibold uppercase tracking-[0.1em] ${status.textClass}`}>
        {status.label}
      </td>
      <td className={`px-1.5 py-2 ${workers > 0 ? 'text-hivemind-green' : 'text-hivemind-dim'}`}>
        {workers}
      </td>
      <td className={`px-1.5 py-2 ${tasks > 0 ? 'text-hivemind-yellow' : 'text-hivemind-dim'}`}>{tasks}</td>
      <td className="px-1.5 py-2 text-hivemind-dim">{formatRelativeTime(project.last_activity)}</td>
    </tr>
  );
}
