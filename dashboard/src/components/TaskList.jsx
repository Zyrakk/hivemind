import { useMemo, useState } from 'react';

const filters = [
  { key: 'all', label: 'ALL' },
  { key: 'pending', label: 'PENDING' },
  { key: 'in_progress', label: 'ACTIVE' },
  { key: 'completed', label: 'DONE' },
  { key: 'blocked', label: 'BLOCKED' }
];

const statusStyles = {
  pending: {
    code: 'HLD',
    border: 'border-hivemind-yellow',
    text: 'text-hivemind-yellow'
  },
  in_progress: {
    code: 'ACT',
    border: 'border-hivemind-green',
    text: 'text-hivemind-green'
  },
  completed: {
    code: 'DON',
    border: 'border-hivemind-blue',
    text: 'text-hivemind-blue'
  },
  blocked: {
    code: 'BLK',
    border: 'border-hivemind-red',
    text: 'text-hivemind-red'
  },
  failed: {
    code: 'BLK',
    border: 'border-hivemind-red',
    text: 'text-hivemind-red'
  }
};

function normalizeDependsOn(dependsOn) {
  if (Array.isArray(dependsOn)) {
    return dependsOn;
  }
  return [];
}

function shouldIncludeTask(task, filterKey) {
  if (filterKey === 'all') {
    return true;
  }
  if (filterKey === 'blocked') {
    return task.status === 'blocked' || task.status === 'failed';
  }
  return task.status === filterKey;
}

export default function TaskList({ tasks, workers }) {
  const [activeFilter, setActiveFilter] = useState('all');
  const [expandedTaskID, setExpandedTaskID] = useState(null);

  const workerByID = useMemo(() => {
    const map = new Map();
    workers.forEach((worker) => {
      map.set(worker.id, worker);
    });
    return map;
  }, [workers]);

  const filteredTasks = useMemo(
    () => tasks.filter((task) => shouldIncludeTask(task, activeFilter)),
    [tasks, activeFilter]
  );

  const toggleExpanded = (taskID) => {
    setExpandedTaskID((current) => (current === taskID ? null : taskID));
  };

  return (
    <section className="bg-hivemind-surface px-[18px] py-[14px]">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span className="text-[10px] uppercase tracking-[0.15em] text-hivemind-dim">TASKS</span>

        <div className="flex flex-wrap items-center gap-[2px]">
          {filters.map((filter) => (
            <button
              key={filter.key}
              type="button"
              onClick={() => setActiveFilter(filter.key)}
              className={`cursor-pointer px-2.5 py-1 text-[10px] uppercase tracking-[0.08em] transition-colors duration-150 focus-visible:outline focus-visible:outline-2 focus-visible:outline-hivemind-blue focus-visible:outline-offset-0 ${
                activeFilter === filter.key
                  ? 'bg-hivemind-dim/20 text-hivemind-text'
                  : 'border border-hivemind-border text-hivemind-dim hover:text-hivemind-muted'
              }`}
            >
              {filter.label}
            </button>
          ))}
        </div>
      </div>

      {filteredTasks.length === 0 ? (
        <p className="mt-3 border border-dashed border-hivemind-border px-4 py-5 text-[11px] text-hivemind-dim">
          No tasks
        </p>
      ) : (
        <div className="mt-3">
          {filteredTasks.map((task) => {
            const dependsOn = normalizeDependsOn(task.depends_on);
            const assignedWorker = task.assigned_worker_id
              ? workerByID.get(task.assigned_worker_id)
              : null;
            const expanded = expandedTaskID === task.id;
            const style = statusStyles[task.status] ?? statusStyles.pending;
            const workerLabel = task.assigned_worker_id
              ? `w-${task.assigned_worker_id}`
              : assignedWorker?.session_id
                ? assignedWorker.session_id
                : '';

            return (
              <article key={task.id} className="border-b border-hivemind-border py-2 last:border-b-0">
                <button
                  type="button"
                  onClick={() => toggleExpanded(task.id)}
                  className="w-full cursor-pointer text-left focus-visible:outline focus-visible:outline-2 focus-visible:outline-hivemind-blue focus-visible:outline-offset-0"
                >
                  <div className={`border-l-2 ${style.border} pl-2`}>
                    <div className="flex items-center justify-between gap-2">
                      <p className="truncate text-[12px] font-medium text-hivemind-text">{task.title}</p>
                      <div className="flex shrink-0 items-center gap-2">
                        <span className={`text-[10px] font-semibold uppercase tracking-[0.08em] ${style.text}`}>
                          {style.code}
                        </span>
                        {workerLabel ? (
                          <span className="text-[10px] text-hivemind-dim">{workerLabel}</span>
                        ) : null}
                      </div>
                    </div>
                  </div>
                </button>

                {task.status === 'blocked' || task.status === 'failed' ? (
                  <p className="mt-2 pl-3 text-[10px] uppercase tracking-[0.08em] text-hivemind-red">
                    BLOCKED BY: {dependsOn.length > 0 ? dependsOn.join(', ') : 'PENDING DEPENDENCY'}
                  </p>
                ) : null}

                <div
                  className={`overflow-hidden pl-3 transition-[max-height] duration-200 ${
                    expanded ? 'max-h-[120px]' : 'max-h-0'
                  }`}
                >
                  <p className="mt-2 border-t border-hivemind-border/50 pt-2 text-[11px] leading-relaxed text-hivemind-muted">
                    {task.description || 'No description'}
                  </p>
                </div>
              </article>
            );
          })}
        </div>
      )}
    </section>
  );
}
