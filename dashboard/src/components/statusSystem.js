const projectStatuses = {
  working: {
    color: 'hivemind-green',
    marker: '▸',
    label: 'ACT',
    textClass: 'text-hivemind-green',
    badgeClass: 'border-hivemind-green/35 bg-hivemind-green/[0.08] text-hivemind-green',
    rowAccentClass: 'border-l-hivemind-green',
    hoverBorderClass: 'hover:border-hivemind-green/70',
    hex: '#5fba7d'
  },
  needs_input: {
    color: 'hivemind-yellow',
    marker: '▲',
    label: 'HLD',
    textClass: 'text-hivemind-yellow',
    badgeClass: 'border-hivemind-yellow/35 bg-hivemind-yellow/[0.08] text-hivemind-yellow',
    rowAccentClass: 'border-l-hivemind-yellow',
    hoverBorderClass: 'hover:border-hivemind-yellow/70',
    hex: '#d4a843'
  },
  pending_review: {
    color: 'hivemind-blue',
    marker: '◆',
    label: 'REV',
    textClass: 'text-hivemind-blue',
    badgeClass: 'border-hivemind-blue/35 bg-hivemind-blue/[0.08] text-hivemind-blue',
    rowAccentClass: 'border-l-hivemind-blue',
    hoverBorderClass: 'hover:border-hivemind-blue/70',
    hex: '#6b8fc7'
  },
  blocked: {
    color: 'hivemind-red',
    marker: '■',
    label: 'BLK',
    textClass: 'text-hivemind-red',
    badgeClass: 'border-hivemind-red/35 bg-hivemind-red/[0.08] text-hivemind-red',
    rowAccentClass: 'border-l-hivemind-red',
    hoverBorderClass: 'hover:border-hivemind-red/70',
    hex: '#c75a5a'
  },
  paused: {
    color: 'hivemind-gray',
    marker: '○',
    label: 'OFF',
    textClass: 'text-hivemind-gray',
    badgeClass: 'border-hivemind-gray/35 bg-hivemind-gray/[0.08] text-hivemind-gray',
    rowAccentClass: 'border-l-hivemind-gray',
    hoverBorderClass: 'hover:border-hivemind-gray/70',
    hex: '#4a4a4a'
  }
};

export function getProjectStatus(status) {
  return projectStatuses[status] ?? projectStatuses.paused;
}

export const projectStatusConfig = projectStatuses;
