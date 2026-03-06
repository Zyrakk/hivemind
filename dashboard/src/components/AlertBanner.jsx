const variantStyles = {
  warning: {
    container: 'border-hivemind-yellow/40 bg-hivemind-yellow/[0.08] text-hivemind-yellow',
    marker: '▲ ALERT'
  },
  error: {
    container: 'border-hivemind-red/40 bg-hivemind-red/[0.08] text-hivemind-red',
    marker: '■ ALERT'
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
    <div className={`border px-[18px] py-[10px] ${style.container}`}>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex min-w-0 items-center gap-2">
          <span className="shrink-0 text-[11px] font-semibold uppercase tracking-[0.15em]">{style.marker}</span>
          <p className="truncate text-[12px] leading-tight text-hivemind-muted">{message}</p>
        </div>
        {actionLabel && onAction ? (
          <button
            type="button"
            onClick={onAction}
            className="border border-current px-2.5 py-1.5 text-[11px] uppercase tracking-[0.1em] transition-colors duration-150 hover:bg-hivemind-bg/40"
          >
            {actionLabel}
          </button>
        ) : null}
      </div>
    </div>
  );
}
