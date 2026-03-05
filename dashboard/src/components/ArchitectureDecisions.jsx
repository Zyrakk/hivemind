import { useMemo, useState } from 'react';

const typeStyles = {
  database: {
    code: 'DB',
    left: 'border-l-hivemind-green',
    badge: 'bg-hivemind-green/[0.08] text-hivemind-green'
  },
  api: {
    code: 'API',
    left: 'border-l-hivemind-blue',
    badge: 'bg-hivemind-blue/[0.08] text-hivemind-blue'
  },
  structure: {
    code: 'STR',
    left: 'border-l-hivemind-yellow',
    badge: 'bg-hivemind-yellow/[0.08] text-hivemind-yellow'
  },
  security: {
    code: 'SEC',
    left: 'border-l-hivemind-red',
    badge: 'bg-hivemind-red/[0.08] text-hivemind-red'
  }
};

export default function ArchitectureDecisions({ decisions }) {
  const normalized = useMemo(() => (Array.isArray(decisions) ? decisions : []), [decisions]);
  const [openID, setOpenID] = useState(() => normalized[0]?.id ?? null);

  if (normalized.length === 0) {
    return <p className="border border-dashed border-hivemind-border px-3 py-3 text-[9px] text-hivemind-dim">No architecture decisions recorded</p>;
  }

  return (
    <div className="border-t border-hivemind-border">
      {normalized.map((decision) => {
        const style = typeStyles[decision.type] ?? typeStyles.structure;
        const isOpen = openID === decision.id;

        return (
          <article key={decision.id} className="border-b border-hivemind-border py-1.5">
            <button
              type="button"
              onClick={() => setOpenID((current) => (current === decision.id ? null : decision.id))}
              className="w-full cursor-pointer border-l-2 px-2 py-1 text-left transition-colors duration-150 hover:bg-hivemind-border/20 focus-visible:outline focus-visible:outline-2 focus-visible:outline-hivemind-blue focus-visible:outline-offset-0"
              style={{ borderLeftColor: style.code === 'DB' ? '#5fba7d' : style.code === 'API' ? '#6b8fc7' : style.code === 'STR' ? '#d4a843' : '#c75a5a' }}
            >
              <span className="flex items-center gap-2">
                <span className={`px-[5px] py-[1px] text-[8px] font-semibold uppercase tracking-[0.08em] ${style.badge}`}>
                  {style.code}
                </span>
                <span className="min-w-0 truncate text-[10px] font-medium text-hivemind-text">{decision.title}</span>
              </span>
            </button>

            <div
              className={`overflow-hidden pl-[10px] transition-[max-height] duration-150 ${
                isOpen ? 'max-h-[140px]' : 'max-h-0'
              }`}
            >
              <p className="mt-1 text-[9px] leading-relaxed text-hivemind-muted">{decision.description}</p>
            </div>
          </article>
        );
      })}
    </div>
  );
}
