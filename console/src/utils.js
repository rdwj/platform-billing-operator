export function fmtMoney(v) {
  if (!v || v === '0' || v === '0.000000') return '$0.00';
  const n = parseFloat(v);
  if (isNaN(n)) return '---';
  const abs = Math.abs(n);
  const s = abs >= 1000 ? '$' + abs.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
    : abs >= 1 ? '$' + abs.toFixed(2) : '$' + abs.toFixed(4);
  return n < 0 ? '−' + s : s;
}

export function badgeColor(status) {
  const map = { Active: 'green', Warning: 'gold', Grace: 'gold', Dunning: 'red', Frozen: 'red', Suspended: 'red', Pending: 'grey', Closed: 'grey' };
  return map[status] || 'grey';
}

export function fmtTokenCount(n) {
  if (n == null || isNaN(n)) return '0';
  if (n >= 1e9) return (n / 1e9).toFixed(1) + 'B';
  if (n >= 1e6) return (n / 1e6).toFixed(1) + 'M';
  if (n >= 1e3) return (n / 1e3).toFixed(1) + 'k';
  return String(n);
}

export const USAGE_LEGEND = [
  { label: 'Input', color: '#0066CC' }, { label: 'Output', color: '#009596' },
  { label: 'Reasoning', color: '#5752D1' }, { label: 'Cached', color: '#F0AB00' }
];
