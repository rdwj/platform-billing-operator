import React from 'react';

export const DISPLAY = "'Red Hat Display',sans-serif";
export const MONO = "'Red Hat Mono',monospace";
export const PAL = {
  green: { bg: '#E9F7DF', fg: '#204D00' }, gold: { bg: '#FDF7E7', fg: '#73480B' },
  red: { bg: '#FAEAE8', fg: '#731F00' }, blue: { bg: '#E0F0FF', fg: '#003366' },
  grey: { bg: '#F0F0F0', fg: '#4D4D4D' }, purple: { bg: '#EFEBFA', fg: '#3C3387' }
};

export const Card = ({ style, children }) => (
  <div style={{ background: '#fff', borderRadius: 8, boxShadow: '0 1px 2px rgba(0,0,0,.06)', padding: '16px 18px', ...style }}>{children}</div>
);

export const PageH = ({ title, sub, right }) => (
  <div style={{ display: 'flex', alignItems: 'flex-start', marginBottom: 18 }}>
    <div>
      <h1 style={{ fontFamily: DISPLAY, fontSize: 22, fontWeight: 700, margin: 0 }}>{title}</h1>
      {sub && <p style={{ margin: '4px 0 0', color: '#6a6e73', fontSize: 13 }}>{sub}</p>}
    </div>
    {right && <div style={{ marginLeft: 'auto', display: 'flex', gap: 8 }}>{right}</div>}
  </div>
);

export const SecTitle = ({ children, right, style }) => (
  <div style={{ display: 'flex', alignItems: 'center', ...style }}>
    <div style={{ fontFamily: DISPLAY, fontSize: 15, fontWeight: 600 }}>{children}</div>
    {right && <div style={{ marginLeft: 'auto' }}>{right}</div>}
  </div>
);

export const MiniLabel = ({ children }) => (
  <div style={{ fontSize: 11, fontWeight: 600, color: '#6a6e73', textTransform: 'uppercase', letterSpacing: '.05em' }}>{children}</div>
);

export const Kpi = ({ label, value, delta, deltaColor = '#6a6e73', border }) => (
  <Card style={{ flex: 1, padding: '14px 18px', ...(border ? { borderLeft: `4px solid ${border}` } : {}) }}>
    <MiniLabel>{label}</MiniLabel>
    <div style={{ fontFamily: DISPLAY, fontSize: 25, fontWeight: 700, marginTop: 4 }}>{value}</div>
    {delta && <div style={{ fontSize: 12, marginTop: 3, color: deltaColor }}>{delta}</div>}
  </Card>
);

export const Badge = ({ c = 'grey', children, style }) => (
  <span style={{ display: 'inline-block', padding: '2px 10px', borderRadius: 12, fontSize: 11, fontWeight: 600, background: PAL[c].bg, color: PAL[c].fg, ...style }}>{children}</span>
);

export const Btn = ({ primary, danger, small, onClick, children, style }) => (
  <button onClick={onClick} style={{
    fontFamily: "'Red Hat Text',sans-serif",
    background: primary ? '#0066CC' : '#fff',
    color: primary ? '#fff' : danger ? '#B1380B' : '#0066CC',
    border: primary ? 'none' : `1px solid ${danger ? '#B1380B' : '#0066CC'}`,
    borderRadius: 20, padding: small ? '6px 14px' : '7px 16px', fontSize: small ? 12 : 13,
    fontWeight: 600, cursor: 'pointer', ...style
  }}>{children}</button>
);

export const THead = ({ cols, labels }) => (
  <div style={{ display: 'grid', gridTemplateColumns: cols, gap: 10, padding: '10px 18px', fontSize: 11, fontWeight: 700, color: '#6a6e73', textTransform: 'uppercase', letterSpacing: '.04em', background: '#fafafa' }}>
    {labels.map((l, i) => <div key={i}>{l}</div>)}
  </div>
);

export const TRow = ({ cols, onClick, style, children }) => (
  <div onClick={onClick} style={{ display: 'grid', gridTemplateColumns: cols, gap: 10, padding: '11px 18px', borderTop: '1px solid #f0f0f0', fontSize: 13, alignItems: 'center', cursor: onClick ? 'pointer' : undefined, ...style }}>{children}</div>
);

export const Mono = ({ children, style }) => (
  <div style={{ fontFamily: MONO, fontSize: 12, ...style }}>{children}</div>
);

export const Dim = ({ children, style }) => (
  <div style={{ color: '#6a6e73', fontSize: 12, ...style }}>{children}</div>
);

export const Progress = ({ w, color = '#0066CC', h = 8 }) => (
  <div style={{ height: h, background: '#f0f0f0', borderRadius: h / 2, overflow: 'hidden' }}>
    <div style={{ height: '100%', background: color, width: w }} />
  </div>
);

export const Bars = ({ weeks }) => (
  <div style={{ display: 'flex', alignItems: 'flex-end', gap: 8, height: 130, marginTop: 16 }}>
    {weeks.map(w => (
      <div key={w.label} style={{ flex: 1, display: 'flex', flexDirection: 'column', justifyContent: 'flex-end', alignItems: 'center', gap: 4, height: '100%' }}>
        <div style={{ fontSize: 10, color: '#6a6e73' }}>{w.top}</div>
        <div title={w.tip} style={{ width: '100%', background: '#0066CC', borderRadius: '3px 3px 0 0', height: w.h }} />
        <div style={{ fontSize: 10, color: '#8a8d90' }}>{w.label}</div>
      </div>
    ))}
  </div>
);

export const Stacked = ({ days, legend, title, height = 150 }) => (
  <Card style={{ padding: '18px 20px' }}>
    <div style={{ display: 'flex', alignItems: 'center', gap: 16 }}>
      <div style={{ fontFamily: DISPLAY, fontSize: 15, fontWeight: 600 }}>{title}</div>
      {legend.map(u => (
        <div key={u.label} style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 12, color: '#4d4d4d' }}>
          <div style={{ width: 10, height: 10, borderRadius: 3, background: u.color }} />{u.label}
        </div>
      ))}
    </div>
    <div style={{ display: 'flex', alignItems: 'flex-end', gap: 10, height, marginTop: 16 }}>
      {days.map(d => (
        <div key={d.label} title={d.tip} style={{ flex: 1, display: 'flex', flexDirection: 'column', justifyContent: 'flex-end', alignItems: 'center', gap: 4, height: '100%' }}>
          <div style={{ width: '100%', display: 'flex', flexDirection: 'column', justifyContent: 'flex-end' }}>
            <div style={{ background: '#F0AB00', height: d.h4, borderRadius: '3px 3px 0 0' }} />
            <div style={{ background: '#5752D1', height: d.h3 }} />
            <div style={{ background: '#009596', height: d.h2 }} />
            <div style={{ background: '#0066CC', height: d.h1 }} />
          </div>
          <div style={{ fontSize: 10, color: '#8a8d90' }}>{d.label}</div>
        </div>
      ))}
    </div>
  </Card>
);

export const Pre = ({ children, style }) => (
  <pre style={{ margin: 0, background: '#151515', color: '#d2d2d2', borderRadius: 6, padding: '14px 16px', fontFamily: MONO, fontSize: 11.5, lineHeight: 1.55, overflow: 'auto', ...style }}>{children}</pre>
);
