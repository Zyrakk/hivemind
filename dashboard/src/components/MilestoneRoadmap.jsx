import SectionHeader from './SectionHeader';

const statusStyles = {
  completed: {
    node: 'bg-hivemind-green border-hivemind-green',
    text: 'text-hivemind-text'
  },
  in_progress: {
    node: 'bg-hivemind-yellow border-hivemind-yellow',
    text: 'text-hivemind-yellow'
  },
  pending: {
    node: 'border-hivemind-dim bg-transparent',
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
      <section className="bg-hivemind-surface px-3 py-2.5">
        <SectionHeader label="MILESTONES" color="hivemind-green" />
        <p className="mt-2 border border-dashed border-hivemind-border px-3 py-4 text-[9px] text-hivemind-dim">
          No milestones defined
        </p>
      </section>
    );
  }

  const currentIndex = currentMilestoneIndex(milestones);

  return (
    <section className="bg-hivemind-surface px-3 py-2.5">
      <SectionHeader label="MILESTONES" count={milestones.length} color="hivemind-green" />

      <div className="mt-3 space-y-2 md:hidden">
        {milestones.map((milestone, index) => {
          const style = statusStyles[milestone.status] ?? statusStyles.pending;
          const isCurrent = index === currentIndex;
          const lineClass = milestone.status === 'completed' ? 'bg-hivemind-green/40' : 'bg-hivemind-border';

          return (
            <article key={milestone.name} className="relative pl-5">
              {index < milestones.length - 1 ? (
                <span className={`absolute left-[3px] top-[10px] h-full w-[2px] ${lineClass}`} />
              ) : null}
              <span className={`absolute left-0 top-[2px] h-[8px] w-[8px] border ${style.node}`} />
              <p className={`text-[9px] uppercase tracking-[0.08em] ${isCurrent ? 'text-hivemind-yellow' : style.text}`}>
                {milestone.name}
              </p>
              <p className="text-[8px] text-hivemind-dim">ETA {milestone.eta}</p>
            </article>
          );
        })}
      </div>

      <div className="mt-3 hidden md:flex md:items-start">
        {milestones.map((milestone, index) => {
          const style = statusStyles[milestone.status] ?? statusStyles.pending;
          const isCurrent = index === currentIndex;
          const lineClass = milestone.status === 'completed' ? 'bg-hivemind-green/40' : 'bg-hivemind-border';

          return (
            <div key={milestone.name} className="flex flex-1 flex-col items-center">
              <div className="mb-2 flex w-full items-center">
                <span className={`h-[8px] w-[8px] shrink-0 border ${style.node}`} />
                {index < milestones.length - 1 ? <span className={`ml-1 h-[2px] w-full ${lineClass}`} /> : null}
              </div>
              <p className={`text-center text-[9px] uppercase tracking-[0.08em] ${isCurrent ? 'text-hivemind-yellow' : style.text}`}>
                {milestone.name}
              </p>
              <p className="text-center text-[8px] text-hivemind-dim">ETA {milestone.eta}</p>
            </div>
          );
        })}
      </div>
    </section>
  );
}
