const resultStyles = {
  success: 'bg-hivemind-green/15 text-hivemind-green border-hivemind-green/30',
  partial: 'bg-hivemind-yellow/15 text-hivemind-yellow border-hivemind-yellow/30',
  failed: 'bg-hivemind-red/15 text-hivemind-red border-hivemind-red/30'
};

function formatDate(value) {
  if (!value) {
    return '--';
  }

  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }

  return new Intl.DateTimeFormat('es-ES', {
    day: '2-digit',
    month: '2-digit',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit'
  }).format(date);
}

export default function LastSession({ lastSession }) {
  if (!lastSession) {
    return <p className="text-sm text-hivemind-muted">No hay sesiones registradas</p>;
  }

  const style = resultStyles[lastSession.result] ?? resultStyles.partial;
  const did = Array.isArray(lastSession.did) ? lastSession.did : [];
  const pending = Array.isArray(lastSession.pending) ? lastSession.pending : [];

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <p className="text-xs text-hivemind-muted">{formatDate(lastSession.date)}</p>
          <p className="text-sm font-semibold text-hivemind-text">{lastSession.task}</p>
        </div>
        <span className={`rounded-full border px-2 py-1 text-xs font-semibold ${style}`}>
          {lastSession.result}
        </span>
      </div>

      <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
        <div>
          <p className="mb-2 text-sm font-semibold text-hivemind-text">Que se hizo</p>
          {did.length === 0 ? (
            <p className="text-sm text-hivemind-muted">Sin registros</p>
          ) : (
            <ul className="list-disc space-y-1 pl-5 text-sm text-hivemind-muted">
              {did.map((item) => (
                <li key={item}>{item}</li>
              ))}
            </ul>
          )}
        </div>
        <div>
          <p className="mb-2 text-sm font-semibold text-hivemind-text">Pendiente</p>
          {pending.length === 0 ? (
            <p className="text-sm text-hivemind-muted">Nada pendiente</p>
          ) : (
            <ul className="list-disc space-y-1 pl-5 text-sm text-hivemind-muted">
              {pending.map((item) => (
                <li key={item}>{item}</li>
              ))}
            </ul>
          )}
        </div>
      </div>
    </div>
  );
}
