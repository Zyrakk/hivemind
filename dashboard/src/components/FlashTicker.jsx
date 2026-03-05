import { useEffect, useMemo, useState } from 'react';

const eventStyleMap = {
  worker_started: {
    hex: '#6b8fc7',
    dotClass: 'bg-hivemind-blue',
    textClass: 'text-hivemind-blue'
  },
  worker_failed: {
    hex: '#c75a5a',
    dotClass: 'bg-hivemind-red',
    textClass: 'text-hivemind-red'
  },
  task_completed: {
    hex: '#5fba7d',
    dotClass: 'bg-hivemind-green',
    textClass: 'text-hivemind-green'
  },
  pr_created: {
    hex: '#5fba7d',
    dotClass: 'bg-hivemind-green',
    textClass: 'text-hivemind-green'
  },
  input_needed: {
    hex: '#d4a843',
    dotClass: 'bg-hivemind-yellow',
    textClass: 'text-hivemind-yellow'
  },
  default: {
    hex: '#777777',
    dotClass: 'bg-hivemind-muted',
    textClass: 'text-hivemind-muted'
  }
};

function formatTimestamp(value) {
  if (!value) {
    return '--:--:--';
  }

  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return '--:--:--';
  }

  return new Intl.DateTimeFormat('en-GB', {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit'
  }).format(date);
}

function eventIdentity(event) {
  if (!event) {
    return '';
  }

  return `${event.timestamp ?? ''}|${event.description ?? ''}|${event.event_type ?? ''}`;
}

export default function FlashTicker({ event, eventCount = 0 }) {
  const [flashKey, setFlashKey] = useState(0);
  const [reduceMotion, setReduceMotion] = useState(false);

  useEffect(() => {
    if (typeof window === 'undefined' || !window.matchMedia) {
      return undefined;
    }

    const mediaQuery = window.matchMedia('(prefers-reduced-motion: reduce)');
    const sync = () => setReduceMotion(mediaQuery.matches);
    sync();

    if (typeof mediaQuery.addEventListener === 'function') {
      mediaQuery.addEventListener('change', sync);
      return () => mediaQuery.removeEventListener('change', sync);
    }

    mediaQuery.addListener(sync);
    return () => mediaQuery.removeListener(sync);
  }, []);

  const style = useMemo(
    () => eventStyleMap[event?.event_type] ?? eventStyleMap.default,
    [event?.event_type]
  );

  useEffect(() => {
    if (!event || reduceMotion) {
      return;
    }

    setFlashKey((current) => current + 1);
  }, [reduceMotion, eventIdentity(event)]);

  return (
    <div className="relative flex h-[26px] items-center overflow-hidden border-b border-hivemind-border bg-[#0d0d0d] px-4 text-[9px]">
      {event ? (
        <>
          <span
            key={reduceMotion ? 'accent-static' : `accent-${flashKey}`}
            className={`flash-ticker-animate absolute left-0 top-0 h-full w-[2px]`}
            style={{
              backgroundColor: style.hex,
              opacity: reduceMotion ? 0.2 : 1,
              animation: reduceMotion ? undefined : 'flashTickerAccent 1.2s ease-out forwards'
            }}
            aria-hidden="true"
          />

          <span
            key={reduceMotion ? 'bg-static' : `bg-${flashKey}`}
            className="flash-ticker-animate absolute inset-0"
            style={{
              backgroundImage: `linear-gradient(90deg, ${style.hex}14 0%, transparent 40%)`,
              opacity: reduceMotion ? 0 : 1,
              animation: reduceMotion ? undefined : 'flashTickerGradient 1s ease-out'
            }}
            aria-hidden="true"
          />

          <div className="relative z-[1] flex min-w-0 flex-1 items-center gap-2">
            <span className={`h-1 w-1 shrink-0 ${style.dotClass}`} aria-hidden="true" />
            <span className="shrink-0 text-hivemind-dim">{formatTimestamp(event.timestamp)}</span>
            <span
              key={reduceMotion ? 'text-static' : `text-${flashKey}`}
              className={`flash-ticker-animate min-w-0 truncate ${
                reduceMotion ? 'text-hivemind-muted' : style.textClass
              }`}
              style={
                reduceMotion
                  ? undefined
                  : {
                      ['--ticker-color']: style.hex,
                      animation:
                        'flashFadeIn 0.3s ease-out, flashTickerText 1s ease-out forwards'
                    }
              }
              title={event.description}
            >
              {event.description}
            </span>
          </div>
        </>
      ) : (
        <p className="relative z-[1] truncate text-hivemind-dim">waiting for events...</p>
      )}

      <span className="relative z-[1] ml-2 shrink-0 text-hivemind-dim">{eventCount} total</span>
    </div>
  );
}
