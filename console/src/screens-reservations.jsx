import React, { useState, useMemo } from 'react';
import { Card, PageH, Kpi, MiniLabel, Badge, Dim, DISPLAY, MONO, PAL } from './ui.jsx';

/* ── helpers ──────────────────────────────────────────────────────── */

const MONTH_SHORT = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

function monthStart(date) {
  const d = new Date(date);
  return new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), 1));
}

function monthAfter(date) {
  const d = new Date(date);
  return new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth() + 1, 1));
}

function monthLabel(date) {
  return MONTH_SHORT[date.getUTCMonth()] + ' ' + date.getUTCFullYear();
}

/** Build an array of month-start dates spanning from `lo` to `hi` (inclusive). */
function monthRange(lo, hi) {
  const months = [];
  let cur = monthStart(lo);
  const ceiling = monthAfter(hi);
  while (cur < ceiling) {
    months.push(new Date(cur));
    cur = monthAfter(cur);
  }
  // Guarantee at least one month even when lo === hi
  if (months.length === 0) months.push(monthStart(lo));
  return months;
}

function pctOf(date, spanStart, spanMs) {
  if (spanMs === 0) return 0;
  return Math.max(0, Math.min(100, ((date - spanStart) / spanMs) * 100));
}

function fmtDate(iso) {
  const d = new Date(iso);
  return d.toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric', timeZone: 'UTC' });
}

function fmtPrice(p) {
  const n = parseFloat(p);
  return '$' + n.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 });
}

/* ── bar color logic ──────────────────────────────────────────────── */

const CLASS_COLORS = {
  Guaranteed:  '#0066CC',
  Preemptible: '#009596',
};

function barStyle(res) {
  const base = CLASS_COLORS[res.class] || '#0066CC';
  const state = res.state;

  let opacity = 1;
  let textDecoration = 'none';
  if (state === 'Cancelled') { opacity = 0.3; textDecoration = 'line-through'; }
  else if (state === 'Expired') { opacity = 0.4; }

  return { background: base, opacity, textDecoration };
}

/* ── tooltip ──────────────────────────────────────────────────────── */

function Tooltip({ res, x, y }) {
  return (
    <div style={{
      position: 'fixed', left: x + 12, top: y - 8,
      background: '#151515', color: '#d2d2d2', borderRadius: 6,
      padding: '10px 14px', fontSize: 12, lineHeight: 1.6,
      fontFamily: MONO, zIndex: 999, pointerEvents: 'none',
      boxShadow: '0 4px 12px rgba(0,0,0,.25)', maxWidth: 340,
      whiteSpace: 'nowrap',
    }}>
      <div style={{ fontFamily: DISPLAY, fontWeight: 600, fontSize: 13, color: '#fff', marginBottom: 4 }}>{res.name}</div>
      <div>Tenant: <span style={{ color: '#fff' }}>{res.tenant}</span></div>
      <div>SKU: <span style={{ color: '#fff' }}>{res.sku}</span></div>
      <div>Count: <span style={{ color: '#fff' }}>{res.count} GPUs</span></div>
      <div>Class: <span style={{ color: '#fff' }}>{res.class}</span></div>
      <div>Monthly: <span style={{ color: '#fff' }}>{fmtPrice(res.monthlyPrice)}</span></div>
      <div>Window: <span style={{ color: '#fff' }}>{fmtDate(res.window.start)} - {fmtDate(res.window.end)}</span></div>
      <div>State: <span style={{ color: '#fff' }}>{res.state}</span></div>
    </div>
  );
}

/* ── main component ───────────────────────────────────────────────── */

export default function CapacityCalendar({ reservations }) {
  const [hover, setHover] = useState(null);   // { res, x, y }

  /* ── KPI calculations ─────────────────────────────────────────── */
  const totalCount = reservations.length;
  const activeGpus = reservations
    .filter(r => r.state === 'Active' || r.state === 'Confirmed')
    .reduce((sum, r) => sum + r.count, 0);
  const guaranteed = reservations.filter(r => r.class === 'Guaranteed').reduce((s, r) => s + r.count, 0);
  const preemptible = reservations.filter(r => r.class === 'Preemptible').reduce((s, r) => s + r.count, 0);

  /* ── timeline span ────────────────────────────────────────────── */
  const { groups, months, spanStart, spanMs } = useMemo(() => {
    if (reservations.length === 0) return { groups: [], months: [], spanStart: 0, spanMs: 0 };

    // Date range from data
    let minT = Infinity, maxT = -Infinity;
    for (const r of reservations) {
      const s = new Date(r.window.start).getTime();
      const e = new Date(r.window.end).getTime();
      if (s < minT) minT = s;
      if (e > maxT) maxT = e;
    }

    const months = monthRange(new Date(minT), new Date(maxT));
    // The timeline spans from the first month's start to the last month's end
    const spanStart = months[0].getTime();
    const spanEnd = monthAfter(months[months.length - 1]).getTime();
    const spanMs = spanEnd - spanStart;

    // Group reservations by sku + geo
    const groupMap = {};
    for (const r of reservations) {
      const key = r.sku + ' · ' + r.geo;
      if (!groupMap[key]) groupMap[key] = [];
      groupMap[key].push(r);
    }
    const groups = Object.entries(groupMap).sort(([a], [b]) => a.localeCompare(b));

    return { groups, months, spanStart, spanMs };
  }, [reservations]);

  /* ── empty state ──────────────────────────────────────────────── */
  if (reservations.length === 0) {
    return (
      <div>
        <PageH title="Capacity calendar" sub="GPU reservations by SKU and geography — timeline view." />
        <Card><Dim>No GPU reservations found.</Dim></Card>
      </div>
    );
  }

  /* ── month column widths (equal) ──────────────────────────────── */
  const monthWidths = months.map(m => {
    const mStart = m.getTime();
    const mEnd = monthAfter(m).getTime();
    return { label: monthLabel(m), leftPct: pctOf(mStart, spanStart, spanMs), widthPct: ((mEnd - mStart) / spanMs) * 100 };
  });

  const ROW_HEIGHT = 40;
  const LABEL_WIDTH = 200;

  return (
    <div>
      <PageH title="Capacity calendar" sub="GPU reservations by SKU and geography — timeline view." />

      {/* KPIs */}
      <div style={{ display: 'flex', gap: 14, marginBottom: 14 }}>
        <Kpi label="Total reservations" value={totalCount} border="#0066CC" />
        <Kpi label="Active GPUs" value={activeGpus} border="#3D7317" />
        <Kpi label="Guaranteed / Preemptible" value={`${guaranteed} / ${preemptible}`} border="#009596" />
      </div>

      {/* Timeline card */}
      <Card style={{ padding: '18px 20px', overflow: 'hidden' }}>
        {/* Header row: blank label column + month headers */}
        <div style={{ display: 'flex' }}>
          <div style={{ width: LABEL_WIDTH, flexShrink: 0 }} />
          <div style={{ flex: 1, position: 'relative', height: 24 }}>
            {monthWidths.map((m, i) => (
              <div key={i} style={{
                position: 'absolute', left: m.leftPct + '%', width: m.widthPct + '%',
                textAlign: 'center', borderLeft: '1px solid #e8e8e8',
                height: '100%', display: 'flex', alignItems: 'center', justifyContent: 'center',
              }}>
                <MiniLabel>{m.label}</MiniLabel>
              </div>
            ))}
          </div>
        </div>

        {/* Separator */}
        <div style={{ borderTop: '1px solid #e0e0e0', marginTop: 4, marginBottom: 4 }} />

        {/* Rows: one per group */}
        {groups.map(([groupKey, items]) => {
          // Stack overlapping bars within the group
          const lanes = [];
          const sorted = [...items].sort((a, b) => new Date(a.window.start) - new Date(b.window.start));
          const laneAssignments = [];

          for (const res of sorted) {
            const rStart = new Date(res.window.start).getTime();
            let placed = false;
            for (let li = 0; li < lanes.length; li++) {
              if (rStart >= lanes[li]) {
                lanes[li] = new Date(res.window.end).getTime();
                laneAssignments.push({ res, lane: li });
                placed = true;
                break;
              }
            }
            if (!placed) {
              lanes.push(new Date(res.window.end).getTime());
              laneAssignments.push({ res, lane: lanes.length - 1 });
            }
          }

          const laneCount = Math.max(1, lanes.length);
          const BAR_H = 26;
          const BAR_GAP = 4;
          const rowH = laneCount * (BAR_H + BAR_GAP) + 8;

          return (
            <div key={groupKey} style={{ display: 'flex', borderTop: '1px solid #f0f0f0' }}>
              {/* Row label */}
              <div style={{
                width: LABEL_WIDTH, flexShrink: 0,
                padding: '8px 12px 8px 0',
                fontFamily: MONO, fontSize: 11.5, color: '#4d4d4d',
                display: 'flex', alignItems: 'center',
              }}>
                {groupKey}
              </div>

              {/* Timeline area */}
              <div style={{ flex: 1, position: 'relative', height: rowH }}>
                {/* Month grid lines */}
                {monthWidths.map((m, i) => (
                  <div key={i} style={{
                    position: 'absolute', left: m.leftPct + '%',
                    top: 0, bottom: 0, borderLeft: '1px solid #f5f5f5',
                  }} />
                ))}

                {/* Reservation bars */}
                {laneAssignments.map(({ res, lane }) => {
                  const rStart = new Date(res.window.start).getTime();
                  const rEnd = new Date(res.window.end).getTime();
                  const left = pctOf(rStart, spanStart, spanMs);
                  const width = pctOf(rEnd, spanStart, spanMs) - left;
                  const { background, opacity, textDecoration } = barStyle(res);
                  const top = 4 + lane * (BAR_H + BAR_GAP);

                  return (
                    <div
                      key={res.name}
                      onMouseEnter={e => setHover({ res, x: e.clientX, y: e.clientY })}
                      onMouseMove={e => setHover({ res, x: e.clientX, y: e.clientY })}
                      onMouseLeave={() => setHover(null)}
                      style={{
                        position: 'absolute',
                        left: left + '%',
                        width: Math.max(width, 0.5) + '%',
                        top,
                        height: BAR_H,
                        background,
                        opacity,
                        borderRadius: 4,
                        display: 'flex',
                        alignItems: 'center',
                        padding: '0 8px',
                        overflow: 'hidden',
                        cursor: 'default',
                      }}
                    >
                      <span style={{
                        fontSize: 11, fontWeight: 600, color: '#fff',
                        whiteSpace: 'nowrap', textDecoration,
                      }}>
                        {res.tenant} &times; {res.count}
                      </span>
                    </div>
                  );
                })}
              </div>
            </div>
          );
        })}
      </Card>

      {/* Legend */}
      <div style={{ display: 'flex', gap: 20, marginTop: 12, padding: '0 4px' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 12, color: '#4d4d4d' }}>
          <div style={{ width: 14, height: 14, borderRadius: 3, background: CLASS_COLORS.Guaranteed }} />
          Guaranteed
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 12, color: '#4d4d4d' }}>
          <div style={{ width: 14, height: 14, borderRadius: 3, background: CLASS_COLORS.Preemptible }} />
          Preemptible
        </div>
      </div>

      {/* Tooltip (follows cursor) */}
      {hover && <Tooltip res={hover.res} x={hover.x} y={hover.y} />}
    </div>
  );
}
