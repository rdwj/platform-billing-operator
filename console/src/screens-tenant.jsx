import React from 'react';
import { Card, PageH, SecTitle, MiniLabel, Kpi, Badge, Btn, THead, TRow, Mono, Dim, Progress, Stacked, DISPLAY, MONO } from './ui.jsx';
import { LedgerTable, BudgetsPanel, KeysTable } from './screens-provider.jsx';

export function TOverview({ D, th, ledger, goTo }) {
  const tKpis = [
    { label: 'Prepaid balance', value: th.balance, delta: th.runway },
    { label: 'Spend · MTD', value: '$61,447', delta: '+31% vs June pace', deltaColor: '#204D00' },
    { label: 'Tokens · MTD', value: '7.1B', delta: '4.6B in · 2.1B out · 388M reasoning' },
    { label: 'Rate limit headroom', value: '1.24M / 2M', delta: 'TPM this minute · Priority plan' }
  ];
  return (
    <div data-screen-label="Tenant overview">
      <PageH title="Overview" sub="Helix Biotech · Priority plan · served from us-east-1 + eu-central-1" />
      <div style={{ display: 'flex', gap: 14, marginBottom: 14 }}>{tKpis.map(k => <Kpi key={k.label} {...k} />)}</div>
      <div style={{ display: 'grid', gridTemplateColumns: '1.6fr 1fr', gap: 14 }}>
        <Stacked days={D.days} legend={D.usageLegend} title="Your tokens by type, daily" />
        <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
          <Card>
            <SecTitle right={<a href="#" onClick={e => { e.preventDefault(); goTo('t-budgets'); }} style={{ fontSize: 12 }}>Manage</a>}>Budgets — July</SecTitle>
            {D.budgets.map(b => (
              <div key={b.name} style={{ padding: '8px 0', borderTop: '1px solid #f5f5f5' }}>
                <div style={{ display: 'flex', fontSize: 12.5 }}>
                  <div style={{ fontWeight: 600 }}>{b.name}</div>
                  <Mono style={{ marginLeft: 'auto', fontSize: 11.5 }}>{b.used}</Mono>
                </div>
                <div style={{ marginTop: 6 }}><Progress w={b.w} color={b.color} h={6} /></div>
              </div>
            ))}
          </Card>
          <Card style={{ flex: 1 }}>
            <SecTitle right={<a href="#" onClick={e => { e.preventDefault(); goTo('t-billing'); }} style={{ fontSize: 12 }}>Ledger</a>}>Recent activity</SecTitle>
            {ledger.slice(0, 4).map((l, i) => (
              <div key={i} style={{ display: 'flex', gap: 8, padding: '7px 0', borderTop: '1px solid #f5f5f5', fontSize: 12, alignItems: 'center' }}>
                <Badge c={l.c} style={{ padding: '1px 8px', fontSize: 10.5 }}>{l.type}</Badge>
                <div style={{ flex: 1, color: '#6a6e73' }}>{l.date}</div>
                <Mono style={{ fontSize: 11.5, color: l.amtColor }}>{l.amt}</Mono>
              </div>
            ))}
          </Card>
        </div>
      </div>
    </div>
  );
}

export function TModels({ D }) {
  const cols = '1.9fr 1.4fr 1.4fr .7fr .7fr .8fr .7fr 1.2fr';
  return (
    <div data-screen-label="Tenant model directory">
      <PageH title="Model directory" sub="Your prices on the Priority plan — volume tier −8% applied to hosted models. Rates per 1M tokens." />
      <Card style={{ padding: 0, overflow: 'hidden' }}>
        <THead cols={cols} labels={['Model', 'Serving', 'Geos', 'Input', 'Output', 'Reasoning', 'Cached', 'Your rate']} />
        {D.tModels.map(m => (
          <TRow key={m.name} cols={cols}>
            <div style={{ fontWeight: 600 }}>{m.name}</div><Dim>{m.kind}</Dim><Dim>{m.geos}</Dim>
            <Mono>{m.pin}</Mono><Mono>{m.pout}</Mono><Mono>{m.prsn}</Mono><Mono>{m.pcache}</Mono>
            <div style={{ fontSize: 11.5, color: '#6a6e73' }}>{m.note}</div>
          </TRow>
        ))}
      </Card>
      <div style={{ background: '#151515', borderRadius: 8, padding: '14px 18px', marginTop: 14, display: 'flex', alignItems: 'center', gap: 14 }}>
        <div style={{ fontFamily: MONO, fontSize: 12.5, color: '#d2d2d2' }}>https://api.corvid-ai.cloud/v1 &nbsp;·&nbsp; OpenAI-compatible · existing SDKs work unchanged</div>
        <a href="#" onClick={e => e.preventDefault()} style={{ marginLeft: 'auto', fontSize: 12, color: '#73BCF7' }}>Copy base URL</a>
      </div>
    </div>
  );
}

export function TUsage({ D }) {
  const cols = '1.3fr 1.8fr .9fr 1fr 1fr 1fr 1fr';
  return (
    <div data-screen-label="Tenant usage & reports">
      <PageH
        title="Usage & reports"
        sub={<span>Per-request metering, deduplicated — quote <span style={{ fontFamily: MONO, fontSize: 12 }}>x-maas-request-id</span> in any dispute.</span>}
        right={<><Btn small>Export CSV</Btn><Btn small>Export FOCUS 1.2</Btn></>}
      />
      <div style={{ marginBottom: 14 }}><Stacked days={D.days} legend={D.usageLegend} title="Tokens by type, daily" height={140} /></div>
      <Card style={{ padding: 0, overflow: 'hidden' }}>
        <THead cols={cols} labels={['API key', 'Model', 'Requests', 'Input', 'Output', 'Reasoning', 'Billed']} />
        {D.tKeyUsage.map(u => (
          <TRow key={u.key} cols={cols}>
            <div style={{ fontWeight: 600 }}>{u.key}</div><Dim>{u.model}</Dim>
            <Mono>{u.req}</Mono><Mono>{u.tin}</Mono><Mono>{u.tout}</Mono><Mono>{u.trsn}</Mono><Mono>{u.cost}</Mono>
          </TRow>
        ))}
      </Card>
      <Dim style={{ marginTop: 10 }}>FOCUS export lets your FinOps team ingest this into Cloudability / CloudHealth alongside cloud spend. Chargeback mapping (key → project → cost center) is set per key.</Dim>
    </div>
  );
}

const InputBox = ({ label, mono, children }) => (
  <div>
    <MiniLabel>{label}</MiniLabel>
    <div style={{ fontFamily: mono ? MONO : undefined, marginTop: 4, border: '1px solid #c7c7c7', borderRadius: 6, padding: '6px 10px', fontSize: 13 }}>{children}</div>
  </div>
);

export function TBilling({ D, th, ledger, arOn, toggleAr }) {
  return (
    <div data-screen-label="Tenant balance & billing">
      <PageH title="Balance & billing" sub="Prepaid balance with auto-reload. Receipts and the full ledger below." />
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1.2fr 1fr', gap: 14, marginBottom: 14 }}>
        <Card style={{ padding: '18px 20px' }}>
          <MiniLabel>Prepaid balance</MiniLabel>
          <div style={{ fontFamily: DISPLAY, fontSize: 32, fontWeight: 700, marginTop: 6 }}>{th.balance}</div>
          <Dim style={{ marginTop: 4 }}>{th.runway}</Dim>
          <div style={{ marginTop: 12 }}><Progress w={th.gaugeW} color={th.gaugeColor} /></div>
          <div style={{ marginTop: 14 }}><Btn primary>Add funds</Btn></div>
        </Card>
        <Card style={{ padding: '18px 20px' }}>
          <div style={{ display: 'flex', alignItems: 'center' }}>
            <div style={{ fontFamily: DISPLAY, fontSize: 15, fontWeight: 600 }}>Auto-reload</div>
            <div style={{ marginLeft: 'auto', fontSize: 12, color: '#6a6e73', marginRight: 8 }}>{arOn ? 'Enabled' : 'Off'}</div>
            <div onClick={toggleAr} style={{ width: 38, height: 20, borderRadius: 10, background: arOn ? '#3D7317' : '#b8bbbe', cursor: 'pointer', position: 'relative', transition: 'background .15s' }}>
              <div style={{ width: 16, height: 16, borderRadius: '50%', background: '#fff', position: 'absolute', top: 2, left: arOn ? 20 : 2, transition: 'left .15s' }} />
            </div>
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10, marginTop: 14 }}>
            <InputBox label="When balance falls below" mono>{th.arThreshold}</InputBox>
            <InputBox label="Reload amount" mono>{th.arAmount}</InputBox>
            <InputBox label="Payment method">{th.arMethod}</InputBox>
            <InputBox label="Monthly hard cap" mono>{th.arCap}</InputBox>
          </div>
          <div style={{ fontSize: 12, color: '#6a6e73', marginTop: 12, borderTop: '1px solid #f0f0f0', paddingTop: 10 }}>
            {arOn ? th.arLast : 'Auto-reload is off — service pauses when balance reaches $0. Alerts still fire at your thresholds.'}
          </div>
        </Card>
        <Card style={{ padding: '18px 20px' }}>
          <div style={{ fontFamily: DISPLAY, fontSize: 15, fontWeight: 600, marginBottom: 10 }}>Receipts</div>
          {D.receipts.map(r => (
            <div key={r.label} style={{ display: 'flex', gap: 8, padding: '8px 0', borderTop: '1px solid #f5f5f5', fontSize: 12.5, alignItems: 'center' }}>
              <div style={{ flex: 1 }}>{r.label}</div><Mono>{r.amt}</Mono>
              <a href="#" onClick={e => e.preventDefault()} style={{ fontSize: 12 }}>PDF</a>
            </div>
          ))}
          <div style={{ fontSize: 12, color: '#6a6e73', marginTop: 12, borderTop: '1px solid #f0f0f0', paddingTop: 10 }}>
            Need invoicing instead of prepaid? <a href="#" onClick={e => e.preventDefault()}>Talk to sales about Enterprise (Net-30, PO).</a>
          </div>
        </Card>
      </div>
      <LedgerTable ledger={ledger} />
    </div>
  );
}

export function TBudgets({ D }) {
  return (
    <div data-screen-label="Tenant budgets & alerts">
      <PageH title="Budgets & alerts" sub="Soft caps notify; hard caps throttle keys at 100%. Budgets reset monthly." />
      <BudgetsPanel D={D} addButtons />
    </div>
  );
}

export function TKeys({ D, keys, createKey, secret, dismiss }) {
  return (
    <div data-screen-label="Tenant API keys">
      <PageH title="API keys" sub="Scopes limit which models and geos a key can call. Per-key limits sit under your plan limits." />
      {secret && (
        <div style={{ background: '#E9F7DF', border: '1px solid #3D7317', borderRadius: 8, padding: '12px 16px', marginBottom: 12, display: 'flex', alignItems: 'center', gap: 12, fontSize: 13 }}>
          <div><b>Key created.</b> Copy it now — it will not be shown again: <span style={{ fontFamily: MONO, fontSize: 12, background: '#fff', padding: '2px 8px', borderRadius: 4 }}>{secret}</span></div>
          <a href="#" onClick={e => { e.preventDefault(); dismiss(); }} style={{ marginLeft: 'auto', fontSize: 12 }}>Dismiss</a>
        </div>
      )}
      <Card style={{ padding: 0, overflow: 'hidden' }}>
        <div style={{ display: 'flex', alignItems: 'center', padding: '14px 18px' }}>
          <div style={{ fontFamily: DISPLAY, fontSize: 15, fontWeight: 600 }}>Keys</div>
          <div style={{ marginLeft: 'auto' }}><Btn primary small onClick={createKey}>Create key</Btn></div>
        </div>
        <KeysTable keys={keys} />
      </Card>
    </div>
  );
}
