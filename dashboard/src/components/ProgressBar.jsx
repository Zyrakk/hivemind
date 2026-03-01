function clamp(value) {
  if (Number.isNaN(value) || !Number.isFinite(value)) {
    return 0;
  }
  if (value < 0) {
    return 0;
  }
  if (value > 100) {
    return 100;
  }
  return value;
}

function normalizeProgress(progress) {
  if (typeof progress !== 'number') {
    return 0;
  }
  if (progress <= 1) {
    return clamp(progress * 100);
  }
  return clamp(progress);
}

function colorClass(percent) {
  if (percent < 30) {
    return 'bg-hivemind-red';
  }
  if (percent <= 70) {
    return 'bg-hivemind-yellow';
  }
  return 'bg-hivemind-green';
}

export default function ProgressBar({ label, progress }) {
  const percent = Math.round(normalizeProgress(progress));

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between gap-3 text-sm">
        <p className="truncate font-medium text-hivemind-text">{label}</p>
        <p className="font-semibold text-hivemind-muted">{percent}%</p>
      </div>

      <div className="h-3 w-full overflow-hidden rounded-full bg-slate-700">
        <div
          className={`h-full rounded-full transition-all duration-700 ease-out ${colorClass(percent)}`}
          style={{ width: `${percent}%` }}
          role="progressbar"
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={percent}
          aria-label={`${label}: ${percent}%`}
        />
      </div>
    </div>
  );
}
