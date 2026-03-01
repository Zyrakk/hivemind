const statusStyles = {
  completed: {
    dot: 'bg-hivemind-green border-hivemind-green',
    text: 'text-hivemind-green'
  },
  in_progress: {
    dot: 'bg-hivemind-yellow border-hivemind-yellow',
    text: 'text-hivemind-yellow'
  },
  pending: {
    dot: 'bg-slate-700 border-slate-500',
    text: 'text-hivemind-muted'
  }
};

function currentMilestoneIndex(milestones) {
  const inProgress = milestones.findIndex((item) => item.status === 'in_progress');
  if (inProgress >= 0) {
    return inProgress;
  }

  const pending = milestones.findIndex((item) => item.status === 'pending');
  if (pending >= 0) {
    return pending;
  }

  return milestones.length - 1;
}

export default function MilestoneRoadmap({ milestones }) {
  if (!Array.isArray(milestones) || milestones.length === 0) {
    return (
      <section className="rounded-xl border border-slate-700 bg-hivemind-card p-4 shadow-panel">
        <h2 className="text-lg font-bold text-hivemind-text">Roadmap</h2>
        <p className="mt-3 text-sm text-hivemind-muted">No hay milestones definidos</p>
      </section>
    );
  }

  const currentIndex = currentMilestoneIndex(milestones);

  return (
    <section className="rounded-xl border border-slate-700 bg-hivemind-card p-4 shadow-panel">
      <h2 className="mb-4 text-lg font-bold text-hivemind-text">Roadmap</h2>

      <div className="space-y-4 md:hidden">
        {milestones.map((milestone, index) => {
          const style = statusStyles[milestone.status] ?? statusStyles.pending;
          const isCurrent = index === currentIndex;

          return (
            <article key={milestone.name} className="relative pl-8">
              {index < milestones.length - 1 ? (
                <span className="absolute left-[11px] top-6 h-full w-px bg-slate-700" />
              ) : null}
              <span
                className={`absolute left-0 top-1 h-6 w-6 rounded-full border-2 ${style.dot} ${
                  isCurrent ? 'ring-2 ring-hivemind-blue/50 ring-offset-2 ring-offset-hivemind-card' : ''
                }`}
              />
              <p className={`text-sm font-semibold ${style.text}`}>{milestone.name}</p>
              <p className="text-xs text-hivemind-muted">{milestone.eta}</p>
            </article>
          );
        })}
      </div>

      <div className="hidden md:flex md:items-start md:gap-0">
        {milestones.map((milestone, index) => {
          const style = statusStyles[milestone.status] ?? statusStyles.pending;
          const isCurrent = index === currentIndex;

          return (
            <div key={milestone.name} className="relative flex flex-1 flex-col items-center px-2">
              <div className="mb-3 flex w-full items-center">
                <span
                  className={`h-5 w-5 shrink-0 rounded-full border-2 ${style.dot} ${
                    isCurrent ? 'ring-2 ring-hivemind-blue/50 ring-offset-2 ring-offset-hivemind-card' : ''
                  }`}
                />
                {index < milestones.length - 1 ? (
                  <span className="ml-2 h-0.5 w-full bg-slate-700" />
                ) : null}
              </div>
              <p className={`text-center text-sm font-semibold ${style.text}`}>{milestone.name}</p>
              <p className="text-center text-xs text-hivemind-muted">{milestone.eta}</p>
            </div>
          );
        })}
      </div>
    </section>
  );
}
