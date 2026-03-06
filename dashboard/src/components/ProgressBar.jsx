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

function colorToken(percent) {
  if (percent < 30) {
    return '#c75a5a';
  }
  if (percent <= 70) {
    return '#d4a843';
  }
  return '#5fba7d';
}

export default function ProgressBar({ label, progress }) {
  const percent = Math.round(normalizeProgress(progress));
  const color = colorToken(percent);

  return (
    <div className="space-y-1.5">
      <div className="flex items-center justify-between gap-3 text-[12px]">
        <p className="truncate font-medium text-hivemind-text">{label}</p>
        <p className="tabular-nums text-hivemind-dim">{String(percent).padStart(2, '0')}%</p>
      </div>

      <div className="h-[3px] w-full bg-hivemind-border">
        <div
          className="h-full transition-all duration-700 ease-out"
          style={{
            width: `${percent}%`,
            backgroundColor: color
          }}
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
