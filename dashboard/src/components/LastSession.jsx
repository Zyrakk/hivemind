const resultStyles = {
  success: {
    label: 'OK',
    badge: 'bg-hivemind-green/[0.08] text-hivemind-green'
  },
  partial: {
    label: 'PARTIAL',
    badge: 'bg-hivemind-yellow/[0.08] text-hivemind-yellow'
  },
  failed: {
    label: 'FAIL',
    badge: 'bg-hivemind-red/[0.08] text-hivemind-red'
  }
};

function formatDate(value) {
  if (!value) {
    return '--';
  }

  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }

  return new Intl.DateTimeFormat('en-GB', {
    day: '2-digit',
    month: 'short',
    hour: '2-digit',
    minute: '2-digit'
  }).format(date);
}

export default function LastSession({ lastSession }) {
  if (!lastSession) {
    return <p className="border border-dashed border-hivemind-border px-3 py-3 text-[9px] text-hivemind-dim">No sessions recorded</p>;
  }

  const style = resultStyles[lastSession.result] ?? resultStyles.partial;
  const did = Array.isArray(lastSession.did) ? lastSession.did : [];
  const pending = Array.isArray(lastSession.pending) ? lastSession.pending : [];

  return (
    <div>
      <div className="flex items-center justify-between gap-2">
        <p className="text-[9px] text-hivemind-dim">{formatDate(lastSession.date)}</p>
        <span className={`px-1.5 py-[1px] text-[8px] font-semibold uppercase tracking-[0.08em] ${style.badge}`}>
          {style.label}
        </span>
      </div>

      <p className="mt-1 text-[10px] font-medium text-hivemind-text">{lastSession.task}</p>

      {did.length > 0 ? (
        <ul className="mt-2 space-y-1 text-[9px] text-hivemind-muted">
          {did.map((item) => (
            <li key={item} className="flex items-start gap-1.5">
              <span className="text-hivemind-green">▸</span>
              <span>{item}</span>
            </li>
          ))}
        </ul>
      ) : null}

      {pending.length > 0 ? (
        <ul className="mt-2 space-y-1 text-[9px] text-hivemind-muted">
          {pending.map((item) => (
            <li key={item} className="flex items-start gap-1.5">
              <span className="text-hivemind-yellow">▲</span>
              <span>{item}</span>
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}
