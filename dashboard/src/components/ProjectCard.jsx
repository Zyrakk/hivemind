import { Link } from 'react-router-dom';

const statusConfig = {
  working: {
    label: 'Working',
    border: 'border-l-4 border-l-hivemind-green',
    badge: 'text-hivemind-green bg-hivemind-green/10 border-hivemind-green/30',
    icon: '●'
  },
  needs_input: {
    label: 'Needs Input',
    border: 'border-l-4 border-l-hivemind-yellow',
    badge: 'text-hivemind-yellow bg-hivemind-yellow/10 border-hivemind-yellow/30',
    icon: '!'
  },
  pending_review: {
    label: 'Pending Review',
    border: 'border-l-4 border-l-hivemind-blue',
    badge: 'text-hivemind-blue bg-hivemind-blue/10 border-hivemind-blue/30',
    icon: '<>'
  },
  blocked: {
    label: 'Blocked',
    border: 'border-l-4 border-l-hivemind-red',
    badge: 'text-hivemind-red bg-hivemind-red/10 border-hivemind-red/30',
    icon: '■'
  },
  paused: {
    label: 'Paused',
    border: 'border-l-4 border-l-hivemind-gray',
    badge: 'text-hivemind-gray bg-hivemind-gray/10 border-hivemind-gray/30',
    icon: '∥'
  }
};

function formatRelativeTime(dateValue) {
  if (!dateValue) {
    return 'sin actividad';
  }

  const date = new Date(dateValue);
  if (Number.isNaN(date.getTime())) {
    return 'sin actividad';
  }

  const diffMs = Date.now() - date.getTime();
  const diffMin = Math.max(0, Math.floor(diffMs / 60000));

  if (diffMin < 1) {
    return 'hace menos de 1 min';
  }
  if (diffMin < 60) {
    return `hace ${diffMin} min`;
  }

  const diffHours = Math.floor(diffMin / 60);
  if (diffHours < 24) {
    return `hace ${diffHours} h`;
  }

  const diffDays = Math.floor(diffHours / 24);
  return `hace ${diffDays} d`;
}

export default function ProjectCard({ project }) {
  const status = statusConfig[project.status] ?? statusConfig.paused;

  return (
    <Link
      to={`/project/${project.id}`}
      className={`block rounded-xl border border-slate-700 bg-hivemind-card p-5 shadow-panel transition hover:-translate-y-0.5 hover:border-slate-500 ${status.border}`}
    >
      <div className="flex items-start justify-between gap-3">
        <h3 className="text-xl font-bold text-hivemind-text">{project.name}</h3>
        <span
          className={`inline-flex items-center gap-1 rounded-full border px-2 py-1 text-xs font-semibold ${status.badge}`}
        >
          <span>{status.icon}</span>
          <span>{status.label}</span>
        </span>
      </div>

      <div className="mt-4 grid grid-cols-2 gap-3 text-sm">
        <div>
          <p className="text-hivemind-muted">Workers activos</p>
          <p className="mt-1 text-lg font-semibold">{project.active_workers}</p>
        </div>
        <div>
          <p className="text-hivemind-muted">Tareas pendientes</p>
          <p className="mt-1 text-lg font-semibold">{project.pending_tasks}</p>
        </div>
      </div>

      <p className="mt-4 text-sm text-hivemind-muted">
        Ultima actividad: <span className="text-hivemind-text">{formatRelativeTime(project.last_activity)}</span>
      </p>
    </Link>
  );
}
