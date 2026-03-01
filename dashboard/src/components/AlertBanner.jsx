const variantStyles = {
  warning: {
    container: 'border-hivemind-yellow/50 bg-hivemind-yellow/10 text-hivemind-yellow',
    dot: 'bg-hivemind-yellow'
  },
  error: {
    container: 'border-hivemind-red/50 bg-hivemind-red/10 text-hivemind-red',
    dot: 'bg-hivemind-red'
  }
};

export default function AlertBanner({
  message,
  variant = 'warning',
  actionLabel,
  onAction
}) {
  if (!message) {
    return null;
  }

  const style = variantStyles[variant] ?? variantStyles.warning;

  return (
    <div className={`rounded-xl border px-4 py-3 shadow-panel ${style.container}`}>
      <div className="flex items-center justify-between gap-3">
        <div className="flex items-center gap-3">
          <span className={`h-2.5 w-2.5 animate-pulse rounded-full ${style.dot}`} />
          <p className="text-sm font-semibold md:text-base">{message}</p>
        </div>
        {actionLabel && onAction ? (
          <button
            type="button"
            onClick={onAction}
            className="rounded-md border border-current px-3 py-1 text-xs font-medium transition hover:bg-black/20 md:text-sm"
          >
            {actionLabel}
          </button>
        ) : null}
      </div>
    </div>
  );
}
