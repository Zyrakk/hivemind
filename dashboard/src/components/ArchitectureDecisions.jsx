import { useMemo, useState } from 'react';

const typeStyles = {
  database: { icon: 'DB', color: 'text-hivemind-blue', badge: 'bg-hivemind-blue/15 border-hivemind-blue/30' },
  api: { icon: 'API', color: 'text-hivemind-green', badge: 'bg-hivemind-green/15 border-hivemind-green/30' },
  structure: { icon: 'ARCH', color: 'text-hivemind-yellow', badge: 'bg-hivemind-yellow/15 border-hivemind-yellow/30' },
  security: { icon: 'SEC', color: 'text-hivemind-red', badge: 'bg-hivemind-red/15 border-hivemind-red/30' }
};

export default function ArchitectureDecisions({ decisions }) {
  const normalized = useMemo(() => (Array.isArray(decisions) ? decisions : []), [decisions]);
  const [openID, setOpenID] = useState(() => normalized[0]?.id ?? null);

  if (normalized.length === 0) {
    return <p className="text-sm text-hivemind-muted">No hay decisiones registradas</p>;
  }

  return (
    <div className="space-y-3">
      {normalized.map((decision) => {
        const style = typeStyles[decision.type] ?? typeStyles.structure;
        const isOpen = openID === decision.id;

        return (
          <article key={decision.id} className="rounded-lg border border-slate-700 bg-slate-800/50 p-3">
            <button
              type="button"
              onClick={() => setOpenID((current) => (current === decision.id ? null : decision.id))}
              className="flex w-full items-start justify-between gap-3 text-left"
            >
              <div className="min-w-0">
                <p className="truncate text-sm font-semibold text-hivemind-text">{decision.title}</p>
                <p className="mt-1 text-xs text-hivemind-muted">{decision.type ?? 'structure'}</p>
              </div>
              <span className={`shrink-0 rounded-full border px-2 py-1 text-[10px] font-bold ${style.badge} ${style.color}`}>
                {style.icon}
              </span>
            </button>

            {isOpen ? <p className="mt-3 text-sm text-hivemind-muted">{decision.description}</p> : null}
          </article>
        );
      })}
    </div>
  );
}
