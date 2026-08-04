import React from 'react';
import { Card, PageH, SecTitle, MiniLabel, Kpi, Badge, Btn, THead, TRow, Mono, Dim, Progress, Bars, Stacked, Pre, DISPLAY, MONO, PAL } from './ui.jsx';
import { planYaml, jsonUsage, jsonEvent, jsonWebhook } from './data.js';

export function Overview({ D, onTenant, goMetering }) {
  const deltas = [
    { d: '+12%', c: '#204D00' }, { d: '+31%', c: '#204D00' }, { d: '+4%', c: '#204D00' },
    { d: '−3%', c: '#731F00' }, { d: '+19%', c: '#204D00' }
  ];
  return (
    <div data-screen-label="Revenue overview">
      <PageH title="Revenue overview" sub="July 2026 month-to-date · all geos · USD-settled" />
      <div style={{ display: 'flex', gap: 14, marginBottom: 14 }}>{D.kpis.map(k => <Kpi key={k.label} {...k} />)}</div>
      <div style={{ display: 'grid', gridTemplateColumns: '1.6fr 1fr', gap: 14, marginBottom: 14 }}>
        <Card style={{ padding: '18px 20px' }}>
          <SecTitle right={<Dim>weekly, settled</Dim>}>Usage revenue, trailing 12 weeks</SecTitle>
          <Bars weeks={D.weeks} />
        </Card>
        <Card style={{ padding: '18px 20px', display: 'flex', flexDirection: 'column' }}>
          <SecTitle>Token mix, MTD</SecTitle>
          <div style={{ display: 'flex', height: 14, borderRadius: 7, overflow: 'hidden', marginTop: 16 }}>
            {D.tokenMix.map(t => <div key={t.label} title={t.tip} style={{ width: t.w, background: t.color }} />)}
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginTop: 16 }}>
            {D.tokenMix.map(t => (
              <div key={t.label} style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13 }}>
                <div style={{ width: 10, height: 10, borderRadius: 3, background: t.color }} />
                <div style={{ flex: 1 }}>{t.label}</div>
                <Mono>{t.amt}</Mono>
                <Dim style={{ width: 70, textAlign: 'right' }}>{t.rev}</Dim>
              </div>
            ))}
          </div>
          <div style={{ marginTop: 'auto', paddingTop: 12, fontSize: 12, color: '#6a6e73', borderTop: '1px solid #f0f0f0' }}>
            Reasoning tokens billed at output rate ×1.4 · cached reads ×0.25 input
          </div>
        </Card>
      </div>
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 14 }}>
        <Card>
          <SecTitle style={{ marginBottom: 10 }}>Top tenants (MTD)</SecTitle>
          {D.tenants.slice(0, 5).map((t, i) => (
            <div key={t.tid} style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '7px 0', borderTop: '1px solid #f5f5f5', fontSize: 13 }}>
              <a href="#" onClick={e => { e.preventDefault(); onTenant(t); }} style={{ flex: 1, fontWeight: 500 }}>{t.name}</a>
              <Mono>{t.mtd}</Mono>
              <div style={{ width: 52, textAlign: 'right', fontSize: 12, color: deltas[i].c }}>{deltas[i].d}</div>
            </div>
          ))}
        </Card>
        <Card>
          <SecTitle style={{ marginBottom: 10 }}>Top models (MTD)</SecTitle>
          {D.topModels.map(m => (
            <div key={m.name} style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '7px 0', borderTop: '1px solid #f5f5f5', fontSize: 13 }}>
              <div style={{ flex: 1, fontWeight: 500 }}>{m.name}</div>
              <Mono>{m.rev}</Mono>
              <Dim style={{ width: 56, textAlign: 'right' }}>{m.tok}</Dim>
            </div>
          ))}
        </Card>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
          <Card style={{ padding: '14px 18px', borderLeft: `4px solid ${D.pipeColor}` }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
              <div style={{ width: 9, height: 9, borderRadius: '50%', background: D.pipeColor }} />
              <div style={{ fontFamily: DISPLAY, fontSize: 14, fontWeight: 600 }}>Metering pipeline</div>
              <a href="#" onClick={e => { e.preventDefault(); goMetering(); }} style={{ marginLeft: 'auto', fontSize: 12 }}>Detail</a>
            </div>
            <div style={{ fontSize: 12, color: '#4d4d4d', marginTop: 6 }}>{D.pipeSummary}</div>
          </Card>
          <Card style={{ padding: '14px 18px', flex: 1 }}>
            <div style={{ fontFamily: DISPLAY, fontSize: 14, fontWeight: 600, marginBottom: 8 }}>Needs attention</div>
            {D.alerts.map((a, i) => (
              <div key={i} style={{ display: 'flex', gap: 8, padding: '6px 0', borderTop: '1px solid #f5f5f5', fontSize: 12, lineHeight: 1.4 }}>
                <div style={{ width: 8, height: 8, borderRadius: '50%', background: a.color, marginTop: 4, flexShrink: 0 }} />
                <div>{a.text}</div>
              </div>
            ))}
          </Card>
        </div>
      </div>
    </div>
  );
}

export function Models({ D }) {
  const cols = '2fr 1.4fr 1.3fr .7fr .7fr .8fr .7fr .8fr .7fr .6fr';
  return (
    <div data-screen-label="Models & pricing">
      <PageH title="Model catalog & pricing" sub="A SKU is model × geo × price version. Rates are per 1M tokens." right={<Btn primary>Add model</Btn>} />
      <Card style={{ padding: 0, overflow: 'hidden', marginBottom: 16 }}>
        <THead cols={cols} labels={['Model', 'Serving', 'Geos', 'Input', 'Output', 'Reasoning', 'Cached', 'Margin', 'Status', 'Price bk']} />
        {D.models.map(m => (
          <TRow key={m.name} cols={cols}>
            <div style={{ fontWeight: 600 }}>{m.name}</div><Dim>{m.kind}</Dim><Dim>{m.geos}</Dim>
            <Mono>{m.pin}</Mono><Mono>{m.pout}</Mono><Mono>{m.prsn}</Mono><Mono>{m.pcache}</Mono>
            <div style={{ fontSize: 12 }}>{m.margin}</div>
            <div><Badge c={m.st}>{m.status}</Badge></div>
            <Dim>{m.ver}</Dim>
          </TRow>
        ))}
      </Card>
      <Card style={{ padding: 20 }}>
        <div style={{ display: 'flex', alignItems: 'baseline', gap: 10 }}>
          <div style={{ fontFamily: DISPLAY, fontSize: 16, fontWeight: 700 }}>Price book — llama-4-maverick-fp8</div>
          <Badge c="blue">v2 · effective 2026-03-15</Badge>
          <a href="#" onClick={e => e.preventDefault()} style={{ marginLeft: 'auto', fontSize: 13 }}>Draft v3 (effective 2026-08-01)</a>
        </div>
        <div style={{ display: 'grid', gridTemplateColumns: '1.2fr 1fr 1fr', gap: 20, marginTop: 16 }}>
          <div>
            <MiniLabel>Geo variants</MiniLabel>
            {D.geoRates.map(g => (
              <div key={g.geo} style={{ border: '1px solid #e8e8e8', borderRadius: 6, padding: '10px 14px', margin: '8px 0' }}>
                <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 13, fontWeight: 600 }}>
                  <div>{g.geo}</div><Dim style={{ fontWeight: 400 }}>margin {g.margin}</Dim>
                </div>
                <div style={{ display: 'flex', gap: 14, marginTop: 6, fontFamily: MONO, fontSize: 12, color: '#4d4d4d' }}>
                  <div>in {g.pin}</div><div>out {g.pout}</div><div>rsn {g.prsn}</div><div>cached {g.pcache}</div>
                </div>
              </div>
            ))}
            <Dim>eu-central premium covers Frankfurt GPU cost + data residency.</Dim>
          </div>
          <div>
            <MiniLabel>Volume discounts (monthly)</MiniLabel>
            {D.volTiers.map(v => (
              <div key={v.band} style={{ display: 'flex', justifyContent: 'space-between', padding: '8px 2px', borderBottom: '1px solid #f0f0f0', fontSize: 13 }}>
                <div>{v.band}</div><div style={{ fontWeight: 600 }}>{v.disc}</div>
              </div>
            ))}
            <div style={{ margin: '16px 0 8px' }}><MiniLabel>Committed use</MiniLabel></div>
            {D.committed.map(c => (
              <div key={c.band} style={{ display: 'flex', justifyContent: 'space-between', padding: '8px 2px', borderBottom: '1px solid #f0f0f0', fontSize: 13 }}>
                <div>{c.band}</div><Mono style={{ fontWeight: 600 }}>{c.price}</Mono>
              </div>
            ))}
          </div>
          <div>
            <MiniLabel>Version history</MiniLabel>
            {D.priceVersions.map(p => (
              <div key={p.tag} style={{ display: 'flex', gap: 10, padding: '8px 0', borderBottom: '1px solid #f0f0f0', fontSize: 12, lineHeight: 1.45 }}>
                <Badge c={p.c} style={{ height: 'fit-content' }}>{p.tag}</Badge>
                <div>{p.text}</div>
              </div>
            ))}
            <div style={{ background: '#FDF7E7', borderRadius: 6, padding: '10px 12px', fontSize: 12, color: '#73480B', marginTop: 10 }}>
              4 tenants grandfathered on v2 until 2026-11-01 per contract. Price changes require 30-day notice (webhook <span style={{ fontFamily: MONO }}>price.changed</span>).
            </div>
          </div>
        </div>
      </Card>
    </div>
  );
}

export function Tenants({ D, onTenant }) {
  const cols = '1.8fr .9fr 1fr 1fr 1.6fr .9fr 1fr .9fr';
  return (
    <div data-screen-label="Tenants">
      <PageH title="Tenants" sub="62 active · 4 low balance · 1 frozen · click a tenant to drill in" right={<Btn primary>Invite tenant</Btn>} />
      <Card style={{ padding: 0, overflow: 'hidden' }}>
        <THead cols={cols} labels={['Tenant', 'Plan', 'Billing mode', 'Balance', 'Auto-reload', 'Burn / day', 'MTD spend', 'Status']} />
        {D.tenants.map(t => (
          <TRow key={t.tid} cols={cols} onClick={() => onTenant(t)}>
            <div style={{ fontWeight: 600, color: '#0066CC' }}>{t.name}</div><div>{t.plan}</div><Dim>{t.mode}</Dim>
            <Mono style={{ color: t.balColor }}>{t.balance}</Mono>
            <div style={{ fontSize: 12, color: '#4d4d4d' }}>{t.reload}</div>
            <Mono>{t.burn}</Mono><Mono>{t.mtd}</Mono>
            <div><Badge c={t.st}>{t.status}</Badge></div>
          </TRow>
        ))}
      </Card>
    </div>
  );
}

export function TenantDetail({ D, td, tab, setTab, ledger, back, grantCredit, grantLabel, toggleFreeze, freezeLabel, goPlans }) {
  const tabs = [['balance', 'Balance & auto-reload'], ['budgets', 'Budgets & alerts'], ['keys', 'API keys'], ['chargeback', 'Chargeback']];
  return (
    <div data-screen-label="Tenant detail">
      <div style={{ marginBottom: 14, fontSize: 13 }}><a href="#" onClick={e => { e.preventDefault(); back(); }}>← Tenants</a></div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 4 }}>
        <h1 style={{ fontFamily: DISPLAY, fontSize: 22, fontWeight: 700, margin: 0 }}>{td.name}</h1>
        <Badge c={td.st}>{td.status}</Badge>
        <div style={{ marginLeft: 'auto', display: 'flex', gap: 8 }}>
          <Btn onClick={grantCredit}>{grantLabel}</Btn>
          <Btn danger onClick={toggleFreeze}>{freezeLabel}</Btn>
        </div>
      </div>
      <div style={{ color: '#6a6e73', fontSize: 13, marginBottom: 16 }}>{td.plan} plan · {td.mode} · tenant_id <span style={{ fontFamily: MONO, fontSize: 12 }}>{td.tid}</span></div>
      <div style={{ display: 'flex', gap: 2, borderBottom: '1px solid #d2d2d2', marginBottom: 18 }}>
        {tabs.map(([id, label]) => (
          <div key={id} onClick={() => setTab(id)} style={{ padding: '9px 18px', fontSize: 14, cursor: 'pointer', borderBottom: `3px solid ${tab === id ? '#0066CC' : 'transparent'}`, color: tab === id ? '#151515' : '#6a6e73', fontWeight: tab === id ? 600 : 400 }}>{label}</div>
        ))}
      </div>

      {tab === 'balance' && <>
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1.2fr 1fr', gap: 14, marginBottom: 14 }}>
          <Card style={{ padding: '18px 20px' }}>
            <MiniLabel>Prepaid balance</MiniLabel>
            <div style={{ fontFamily: DISPLAY, fontSize: 32, fontWeight: 700, marginTop: 6 }}>{td.balance}</div>
            <Dim style={{ marginTop: 4 }}>{td.runway}</Dim>
            <div style={{ marginTop: 12 }}><Progress w={td.gaugeW} color={td.gaugeColor} /></div>
            <div style={{ fontSize: 11, color: '#8a8d90', marginTop: 6 }}>funded level vs. reload threshold</div>
          </Card>
          <Card style={{ padding: '18px 20px' }}>
            <SecTitle right={<Badge c={td.arWarn ? 'gold' : 'green'}>{td.arState}</Badge>}>Auto-reload</SecTitle>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10, marginTop: 14, fontSize: 13 }}>
              <div><MiniLabel>When balance falls below</MiniLabel><div style={{ fontFamily: MONO, marginTop: 3 }}>{td.arThreshold}</div></div>
              <div><MiniLabel>Reload amount</MiniLabel><div style={{ fontFamily: MONO, marginTop: 3 }}>{td.arAmount}</div></div>
              <div><MiniLabel>Payment method</MiniLabel><div style={{ marginTop: 3 }}>{td.arMethod}</div></div>
              <div><MiniLabel>Monthly hard cap</MiniLabel><div style={{ fontFamily: MONO, marginTop: 3 }}>{td.arCap}</div></div>
            </div>
            <div style={{ fontSize: 12, color: '#6a6e73', marginTop: 12, borderTop: '1px solid #f0f0f0', paddingTop: 10 }}>{td.arLast}</div>
          </Card>
          <Card style={{ padding: '18px 20px' }}>
            <SecTitle>Enforcement</SecTitle>
            <div style={{ fontSize: 12.5, color: '#4d4d4d', lineHeight: 1.55, marginTop: 10 }}>
              Balance compiles to a rolling 5-minute token allowance pushed to the gateway (TokenRateLimitPolicy). Worst-case unbilled overrun at depletion: <b>≤ 5 min of burn ({td.overrun})</b>, reconciled post-paid.
            </div>
            <div style={{ fontSize: 12, marginTop: 10 }}><a href="#" onClick={e => { e.preventDefault(); goPlans(); }}>View compiled policy →</a></div>
          </Card>
        </div>
        <LedgerTable ledger={ledger} />
      </>}

      {tab === 'budgets' && <BudgetsPanel D={D} />}

      {tab === 'keys' && <>
        <Card style={{ padding: 0, overflow: 'hidden' }}>
          <div style={{ display: 'flex', alignItems: 'center', padding: '14px 18px' }}>
            <div style={{ fontFamily: DISPLAY, fontSize: 15, fontWeight: 600 }}>API keys</div>
            <div style={{ marginLeft: 'auto' }}><Btn primary small>Create key</Btn></div>
          </div>
          <KeysTable keys={D.keys} />
        </Card>
        <div style={{ background: '#E0F0FF', borderRadius: 8, padding: '12px 16px', fontSize: 12.5, color: '#003366', marginTop: 12 }}>
          Keys map to Authorino identities; scopes and per-key limits compile to AuthPolicy claims consumed by TokenRateLimitPolicy predicates.
        </div>
      </>}

      {tab === 'chargeback' && <>
        <Card style={{ padding: 0, overflow: 'hidden' }}>
          <div style={{ display: 'flex', alignItems: 'center', padding: '14px 18px' }}>
            <div style={{ fontFamily: DISPLAY, fontSize: 15, fontWeight: 600 }}>Chargeback / showback — July MTD</div>
            <div style={{ marginLeft: 'auto', display: 'flex', gap: 8 }}>
              <Btn small>Export CSV</Btn><Btn small>Export FOCUS 1.2</Btn><Btn small>Schedule…</Btn>
            </div>
          </div>
          <THead cols="1fr 1.4fr 1fr 1fr 1fr 1fr .8fr" labels={['Cost center', 'Project', 'Input tok', 'Output tok', 'Reasoning', 'Cost', 'Share']} />
          {D.chargeback.map(c => (
            <TRow key={c.project} cols="1fr 1.4fr 1fr 1fr 1fr 1fr .8fr">
              <Mono>{c.cc}</Mono><div style={{ fontWeight: 600 }}>{c.project}</div>
              <Mono>{c.tin}</Mono><Mono>{c.tout}</Mono><Mono>{c.trsn}</Mono><Mono>{c.cost}</Mono>
              <div><Progress w={c.w} h={7} /></div>
            </TRow>
          ))}
        </Card>
        <Dim style={{ marginTop: 10 }}>FOCUS export maps token usage to the FinOps Open Cost & Usage Specification so tenants can ingest into Cloudability, CloudHealth, etc. Attribution key: API key → project → cost center (set per key).</Dim>
      </>}
    </div>
  );
}

export function LedgerTable({ ledger }) {
  const cols = '1.1fr 1fr 2fr 1fr 1fr';
  return (
    <Card style={{ padding: 0, overflow: 'hidden' }}>
      <div style={{ padding: '14px 18px', fontFamily: DISPLAY, fontSize: 15, fontWeight: 600 }}>Ledger</div>
      <THead cols={cols} labels={['Date', 'Type', 'Detail', 'Amount', 'Balance after']} />
      {ledger.map((l, i) => (
        <TRow key={i} cols={cols}>
          <Dim>{l.date}</Dim><div><Badge c={l.c}>{l.type}</Badge></div>
          <div style={{ fontSize: 12, color: '#4d4d4d' }}>{l.detail}</div>
          <Mono style={{ color: l.amtColor }}>{l.amt}</Mono><Mono>{l.after}</Mono>
        </TRow>
      ))}
    </Card>
  );
}

export function KeysTable({ keys }) {
  const cols = '1.2fr 1.2fr 2fr 1fr .9fr .8fr .9fr';
  return (<>
    <THead cols={cols} labels={['Name', 'Key', 'Scopes (models · geos)', 'Per-key limit', 'Last used', 'Status', '']} />
    {keys.map(k => (
      <TRow key={k.name} cols={cols}>
        <div style={{ fontWeight: 600 }}>{k.name}</div><Mono style={{ color: '#6a6e73' }}>{k.prefix}</Mono>
        <div style={{ fontSize: 12, color: '#4d4d4d' }}>{k.scopes}</div><Mono>{k.limit}</Mono><Dim>{k.last}</Dim>
        <div><Badge c={k.st}>{k.status}</Badge></div>
        <div style={{ display: 'flex', gap: 10, fontSize: 12 }}>
          <a href="#" onClick={e => e.preventDefault()}>Rotate</a>
          <a href="#" onClick={e => e.preventDefault()} style={{ color: '#B1380B' }}>Revoke</a>
        </div>
      </TRow>
    ))}
  </>);
}

export function BudgetsPanel({ D, addButtons }) {
  return (
    <div style={{ display: 'grid', gridTemplateColumns: '1.3fr 1fr', gap: 14 }}>
      <Card style={{ padding: '18px 20px' }}>
        <div style={{ display: 'flex', alignItems: 'center', marginBottom: 14 }}>
          <div style={{ fontFamily: DISPLAY, fontSize: 15, fontWeight: 600 }}>Project budgets — July</div>
          {addButtons && <div style={{ marginLeft: 'auto' }}><Btn small>Add budget</Btn></div>}
        </div>
        {D.budgets.map(b => (
          <div key={b.name} style={{ padding: '10px 0', borderTop: '1px solid #f5f5f5' }}>
            <div style={{ display: 'flex', fontSize: 13 }}>
              <div style={{ fontWeight: 600 }}>{b.name}</div>
              <Mono style={{ marginLeft: 'auto' }}>{b.used} / {b.cap}</Mono>
            </div>
            <div style={{ marginTop: 8 }}><Progress w={b.w} color={b.color} /></div>
            <div style={{ fontSize: 11, color: '#6a6e73', marginTop: 5 }}>{b.note}</div>
          </div>
        ))}
      </Card>
      <Card style={{ padding: '18px 20px' }}>
        <div style={{ fontFamily: DISPLAY, fontSize: 15, fontWeight: 600, marginBottom: 8 }}>Alert rules</div>
        {D.alertRules.map(r => (
          <div key={r.when} style={{ display: 'flex', gap: 10, padding: '9px 0', borderTop: '1px solid #f5f5f5', fontSize: 12.5, lineHeight: 1.45 }}>
            <div style={{ flex: 1 }}><b>{r.when}</b><div style={{ color: '#6a6e73', marginTop: 2 }}>{r.then}</div></div>
            <Badge c="green" style={{ height: 'fit-content' }}>On</Badge>
          </div>
        ))}
        <div style={{ marginTop: 12 }}><Btn small>Add rule</Btn></div>
      </Card>
    </div>
  );
}

export function Plans({ D, plan, setPlan }) {
  const sel = D.plans.find(p => p.name === plan) || D.plans[2];
  return (
    <div data-screen-label="Plans & rate limits">
      <PageH title="Plans & rate limits" sub="Plans compile to Kuadrant policy CRs on the MaaS gateway. Select a plan to inspect the generated policy." />
      <div style={{ display: 'flex', gap: 14, marginBottom: 16 }}>
        {D.plans.map(p => (
          <Card key={p.name} style={{ flex: 1, cursor: 'pointer', border: `2px solid ${plan === p.name ? '#0066CC' : 'transparent'}` }}>
            <div onClick={() => setPlan(p.name)}>
              <div style={{ display: 'flex', alignItems: 'baseline' }}>
                <div style={{ fontFamily: DISPLAY, fontSize: 16, fontWeight: 700 }}>{p.name}</div>
                <Dim style={{ marginLeft: 'auto' }}>{p.tenantsN} tenants</Dim>
              </div>
              <div style={{ display: 'flex', flexDirection: 'column', gap: 6, marginTop: 12, fontSize: 12.5, color: '#4d4d4d' }}>
                {[['Requests / min', p.rpm], ['Tokens / min', p.tpm], ['Tokens / day', p.day], ['Concurrency', p.conc]].map(([l, v]) => (
                  <div key={l} style={{ display: 'flex', justifyContent: 'space-between' }}><span>{l}</span><b style={{ fontFamily: MONO, fontWeight: 600 }}>{v}</b></div>
                ))}
              </div>
              <div style={{ borderTop: '1px solid #f0f0f0', marginTop: 12, paddingTop: 10, fontSize: 12, color: '#6a6e73', lineHeight: 1.45 }}>{p.models}<br />{p.queue}</div>
            </div>
          </Card>
        ))}
      </div>
      <div style={{ display: 'grid', gridTemplateColumns: '1.2fr 1fr', gap: 14 }}>
        <div style={{ background: '#151515', borderRadius: 8, padding: '18px 20px', overflow: 'auto' }}>
          <div style={{ display: 'flex', alignItems: 'center', marginBottom: 10 }}>
            <div style={{ color: '#fff', fontFamily: DISPLAY, fontSize: 14, fontWeight: 600 }}>Compiled policy — {sel.name}</div>
            <span style={{ marginLeft: 'auto', fontSize: 11, color: '#8a8d90', fontFamily: MONO }}>maas-default-gateway</span>
          </div>
          <pre style={{ margin: 0, fontFamily: MONO, fontSize: 11.5, lineHeight: 1.55, color: '#d2d2d2', whiteSpace: 'pre' }}>{planYaml(sel)}</pre>
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
          <Card>
            <div style={{ fontFamily: DISPLAY, fontSize: 14, fontWeight: 600, marginBottom: 8 }}>How enforcement layers stack</div>
            {D.enfLayers.map(e => (
              <div key={e.layer} style={{ display: 'flex', gap: 10, padding: '8px 0', borderTop: '1px solid #f5f5f5', fontSize: 12.5, lineHeight: 1.45 }}>
                <div style={{ width: 110, flexShrink: 0, fontWeight: 600 }}>{e.layer}</div>
                <div style={{ color: '#4d4d4d' }}>{e.desc}</div>
              </div>
            ))}
          </Card>
          <div style={{ background: '#FDF7E7', borderRadius: 8, padding: '14px 16px', fontSize: 12.5, color: '#73480B', lineHeight: 1.5 }}>
            <b>Reality check:</b> Limitador counters (Redis-backed for multi-cluster) enforce windows but are not the billing record. The usage ledger is the source of truth; balance enforcement tolerates ≤5 min of drift between them.
          </div>
        </div>
      </div>
    </div>
  );
}

const selStyle = { fontFamily: "'Red Hat Text',sans-serif", fontSize: 13, padding: '7px 10px', border: '1px solid #c7c7c7', borderRadius: 6, background: '#fff' };

export function UsageExplorer({ D }) {
  const cols = '1.8fr 1fr .9fr 1fr 1fr 1fr 1fr 1fr';
  return (
    <div data-screen-label="Usage explorer">
      <PageH title="Usage explorer" sub="Query the usage ledger. Every row is backed by per-request events, deduplicated on request_id." />
      <Card style={{ padding: '12px 16px', display: 'flex', gap: 10, alignItems: 'center', marginBottom: 14, flexWrap: 'wrap' }}>
        <select style={selStyle}><option>Last 14 days</option><option>MTD</option><option>Custom…</option></select>
        <select style={selStyle}><option>All tenants</option><option>Helix Biotech</option><option>Kestrel Financial</option></select>
        <select style={selStyle}><option>All models</option><option>llama-4-maverick-fp8</option><option>granite-4.0-h-small</option></select>
        <select style={selStyle}><option>All geos</option><option>us-east-1</option><option>eu-central-1</option><option>ap-south-1</option></select>
        <select style={selStyle}><option>Group by: model</option><option>Group by: tenant</option><option>Group by: API key</option><option>Group by: geo</option></select>
        <div style={{ marginLeft: 'auto' }}><Btn small>Export</Btn></div>
      </Card>
      <div style={{ marginBottom: 14 }}><Stacked days={D.days} legend={D.usageLegend} title="Tokens by type, daily" /></div>
      <Card style={{ padding: 0, overflow: 'hidden' }}>
        <THead cols={cols} labels={['Model', 'Geo', 'Requests', 'Input', 'Output', 'Reasoning', 'Cached', 'Billed']} />
        {D.usageRows.map((u, i) => (
          <TRow key={i} cols={cols} style={{ fontWeight: u.total ? 700 : 400, background: u.total ? '#fafafa' : '#fff' }}>
            <div>{u.model}</div><Dim>{u.geo}</Dim>
            <Mono>{u.req}</Mono><Mono>{u.tin}</Mono><Mono>{u.tout}</Mono><Mono>{u.trsn}</Mono><Mono>{u.tcache}</Mono><Mono>{u.cost}</Mono>
          </TRow>
        ))}
      </Card>
    </div>
  );
}

export function Billing({ D, dunning }) {
  return (
    <div data-screen-label="Billing operations">
      <PageH title="Billing operations" sub="Rated usage flows to the connected billing provider. Corvid stays the metering source of truth." />
      <div style={{ display: 'flex', gap: 14, marginBottom: 16 }}>
        {D.connectors.map(c => (
          <Card key={c.name} style={{ flex: 1, borderTop: `3px solid ${c.top}` }}>
            <div style={{ display: 'flex', alignItems: 'center' }}>
              <div style={{ fontFamily: DISPLAY, fontSize: 15, fontWeight: 700 }}>{c.name}</div>
              <div style={{ marginLeft: 'auto' }}><Badge c={c.st}>{c.status}</Badge></div>
            </div>
            <div style={{ fontSize: 12.5, color: '#4d4d4d', marginTop: 8, lineHeight: 1.5 }}>{c.desc}</div>
            <div style={{ fontSize: 12, marginTop: 10 }}><a href="#" onClick={e => e.preventDefault()}>{c.action}</a></div>
          </Card>
        ))}
      </div>
      <div style={{ display: 'grid', gridTemplateColumns: '1.2fr 1fr', gap: 14, marginBottom: 14 }}>
        <Card style={{ padding: 0, overflow: 'hidden' }}>
          <div style={{ padding: '14px 18px', fontFamily: DISPLAY, fontSize: 15, fontWeight: 600 }}>Invoice runs</div>
          <THead cols="1fr .9fr 1fr 1fr 1fr" labels={['Period', 'Invoices', 'Amount', 'Status', '']} />
          {D.invoiceRuns.map(i => (
            <TRow key={i.period} cols="1fr .9fr 1fr 1fr 1fr">
              <div style={{ fontWeight: 600 }}>{i.period}</div><Mono>{i.count}</Mono><Mono>{i.amount}</Mono>
              <div><Badge c={i.st}>{i.status}</Badge></div>
              <div style={{ fontSize: 12 }}><a href="#" onClick={e => e.preventDefault()}>{i.action}</a></div>
            </TRow>
          ))}
          <div style={{ padding: '12px 18px', borderTop: '1px solid #f0f0f0', fontSize: 12, color: '#6a6e73' }}>
            Prepaid reloads are recognized as deferred revenue; usage draws recognize on consumption (rev-rec export → Zuora Revenue / NetSuite).
          </div>
        </Card>
        <Card style={{ padding: 0, overflow: 'hidden' }}>
          <div style={{ padding: '14px 18px', fontFamily: DISPLAY, fontSize: 15, fontWeight: 600 }}>Dunning queue</div>
          {dunning.map(d => (
            <div key={d.tenant} style={{ padding: '12px 18px', borderTop: '1px solid #f0f0f0' }}>
              <div style={{ display: 'flex', fontSize: 13, alignItems: 'center' }}>
                <div style={{ fontWeight: 600 }}>{d.tenant}</div>
                <Mono style={{ marginLeft: 'auto' }}>{d.amount}</Mono>
              </div>
              <Dim style={{ marginTop: 4 }}>{d.detail}</Dim>
              <div style={{ display: 'flex', gap: 12, fontSize: 12, marginTop: 6 }}>
                <a href="#" onClick={e => { e.preventDefault(); d.retry && d.retry(); }}>{d.a1}</a>
                <a href="#" onClick={e => e.preventDefault()} style={{ color: '#B1380B' }}>{d.a2}</a>
              </div>
            </div>
          ))}
        </Card>
      </div>
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 14 }}>
        <Card style={{ padding: 0, overflow: 'hidden' }}>
          <div style={{ padding: '14px 18px', fontFamily: DISPLAY, fontSize: 15, fontWeight: 600 }}>Credit grants</div>
          {D.grants.map(g => (
            <div key={g.text} style={{ display: 'flex', gap: 10, padding: '10px 18px', borderTop: '1px solid #f0f0f0', fontSize: 12.5, alignItems: 'center' }}>
              <div style={{ flex: 1 }}>{g.text}</div><Mono>{g.amt}</Mono><div style={{ color: '#6a6e73', fontSize: 11 }}>{g.when}</div>
            </div>
          ))}
        </Card>
        <Card style={{ padding: 0, overflow: 'hidden' }}>
          <div style={{ padding: '14px 18px', fontFamily: DISPLAY, fontSize: 15, fontWeight: 600 }}>Webhook deliveries</div>
          {D.webhookLog.map(w => (
            <div key={w.event + w.when} style={{ display: 'flex', gap: 10, padding: '9px 18px', borderTop: '1px solid #f0f0f0', fontSize: 12, alignItems: 'center' }}>
              <div style={{ fontFamily: MONO, color: '#4d4d4d', flex: 1 }}>{w.event}</div>
              <div style={{ color: '#6a6e73' }}>{w.dest}</div>
              <Badge c={w.st} style={{ padding: '1px 8px', fontSize: 11 }}>{w.code}</Badge>
              <div style={{ color: '#8a8d90', fontSize: 11, width: 52, textAlign: 'right' }}>{w.when}</div>
            </div>
          ))}
        </Card>
      </div>
    </div>
  );
}

export function Cost({ D }) {
  return (
    <div data-screen-label="Cost & margin">
      <PageH title="Cost & margin" sub="GPU fleet cost vs. token revenue, per model. Pass-through spend tracked separately." />
      <div style={{ display: 'flex', gap: 14, marginBottom: 14 }}>{D.costKpis.map(k => <Kpi key={k.label} {...k} />)}</div>
      <div style={{ display: 'grid', gridTemplateColumns: '1.15fr 1fr', gap: 14 }}>
        <Card style={{ padding: 0, overflow: 'hidden' }}>
          <div style={{ padding: '14px 18px', fontFamily: DISPLAY, fontSize: 15, fontWeight: 600 }}>Hosted models — unit economics (per day)</div>
          <THead cols="1.7fr 1fr 1fr .8fr 1.4fr" labels={['Model', 'Revenue', 'Serving cost', 'Margin', '']} />
          {D.modelMargins.map(m => (
            <TRow key={m.name} cols="1.7fr 1fr 1fr .8fr 1.4fr">
              <div style={{ fontWeight: 600 }}>{m.name}</div><Mono>{m.rev}</Mono><Mono>{m.cost}</Mono>
              <div style={{ fontWeight: 600, color: m.mColor }}>{m.margin}</div>
              <div><Progress w={m.w} color={m.mColor} h={7} /></div>
            </TRow>
          ))}
          <div style={{ padding: '12px 18px', borderTop: '1px solid #f0f0f0', fontSize: 12, color: '#6a6e73' }}>
            Serving cost = amortized GPU-hr × utilization share + energy/overhead allocator. qwen3-coder preview is under target margin (52%) — candidates: raise output rate or densify onto shared pool.
          </div>
        </Card>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
          <Card style={{ padding: 0, overflow: 'hidden' }}>
            <div style={{ padding: '14px 18px', fontFamily: DISPLAY, fontSize: 15, fontWeight: 600 }}>GPU fleet</div>
            <THead cols="1.4fr 1fr .9fr .7fr .9fr" labels={['Pool', 'GPUs', 'Region', 'Util', 'Cost/day']} />
            {D.pools.map(p => (
              <TRow key={p.name} cols="1.4fr 1fr .9fr .7fr .9fr" style={{ fontSize: 12.5 }}>
                <Mono style={{ fontWeight: 600 }}>{p.name}</Mono><Dim>{p.gpus}</Dim><Dim>{p.region}</Dim>
                <div style={{ color: p.uColor, fontWeight: 600 }}>{p.util}</div><Mono>{p.cost}</Mono>
              </TRow>
            ))}
          </Card>
          <Card style={{ padding: 0, overflow: 'hidden' }}>
            <div style={{ padding: '14px 18px', fontFamily: DISPLAY, fontSize: 15, fontWeight: 600 }}>Pass-through providers (MTD)</div>
            {D.passThrough.map(p => (
              <div key={p.name} style={{ display: 'flex', gap: 10, padding: '10px 18px', borderTop: '1px solid #f0f0f0', fontSize: 13, alignItems: 'center' }}>
                <div style={{ flex: 1, fontWeight: 600 }}>{p.name}</div>
                <Dim>spend {p.spend}</Dim><Dim>billed {p.billed}</Dim>
                <div style={{ fontWeight: 600, color: '#204D00' }}>{p.margin}</div>
              </div>
            ))}
            <div style={{ padding: '10px 18px', borderTop: '1px solid #f0f0f0', fontSize: 12, color: '#6a6e73' }}>
              Markup 12% + FX buffer. Provider price cuts flow to tenants on next price-book version.
            </div>
          </Card>
        </div>
      </div>
    </div>
  );
}

export function Metering({ D }) {
  return (
    <div data-screen-label="Metering pipeline health">
      <PageH title="Metering pipeline health" sub="Per-request usage events from the gateway, through Loki, into the billing ledger. This page is why tenants trust the invoice." />
      <div style={{ display: 'flex', alignItems: 'stretch', marginBottom: 16 }}>
        {D.stages.map((s, i) => (
          <div key={s.name} style={{ flex: 1, display: 'flex', alignItems: 'stretch' }}>
            <Card style={{ flex: 1, padding: '14px 16px', borderTop: `3px solid ${s.color}` }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 7 }}>
                <div style={{ width: 9, height: 9, borderRadius: '50%', background: s.color }} />
                <div style={{ fontFamily: DISPLAY, fontSize: 13, fontWeight: 700 }}>{s.name}</div>
              </div>
              <div style={{ fontFamily: MONO, fontSize: 16, fontWeight: 600, marginTop: 8, color: s.vColor }}>{s.metric}</div>
              <div style={{ fontSize: 11.5, color: '#6a6e73', marginTop: 4, lineHeight: 1.4 }}>{s.sub}</div>
            </Card>
            {i < D.stages.length - 1 && <div style={{ display: 'flex', alignItems: 'center', padding: '0 6px', color: '#8a8d90', fontSize: 16 }}>→</div>}
          </div>
        ))}
      </div>
      <div style={{ display: 'flex', gap: 14, marginBottom: 16 }}>{D.meterKpis.map(k => <Kpi key={k.label} {...k} />)}</div>
      <div style={{ display: 'grid', gridTemplateColumns: '1.3fr 1fr', gap: 14 }}>
        <Card style={{ padding: 0, overflow: 'hidden' }}>
          <div style={{ padding: '14px 18px', fontFamily: DISPLAY, fontSize: 15, fontWeight: 600 }}>Reconciliation — gateway ledger vs. engine-reported usage</div>
          <THead cols="1fr 1.6fr 1.1fr 1.1fr .8fr .9fr" labels={['Day', 'Model', 'Gateway tok', 'Engine tok', 'Drift', 'Status']} />
          {D.recon.map((r, i) => (
            <TRow key={i} cols="1fr 1.6fr 1.1fr 1.1fr .8fr .9fr" style={{ fontSize: 12.5 }}>
              <Dim>{r.day}</Dim><div style={{ fontWeight: 600 }}>{r.model}</div>
              <Mono>{r.gw}</Mono><Mono>{r.eng}</Mono>
              <Mono style={{ color: r.ok ? '#204D00' : '#73480B' }}>{r.drift}</Mono>
              <div><Badge c={r.ok ? 'green' : 'gold'}>{r.ok ? 'Within SLO' : 'Investigate'}</Badge></div>
            </TRow>
          ))}
        </Card>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
          <Card>
            <div style={{ fontFamily: DISPLAY, fontSize: 14, fontWeight: 600, marginBottom: 8 }}>Edge cases, last 24h</div>
            {D.edgeCases.map(e => (
              <div key={e.label} style={{ display: 'flex', gap: 10, padding: '8px 0', borderTop: '1px solid #f5f5f5', fontSize: 12.5, alignItems: 'baseline' }}>
                <div style={{ flex: 1 }}>{e.label}</div><Mono style={{ fontWeight: 600 }}>{e.value}</Mono>
              </div>
            ))}
          </Card>
          <div style={{ background: '#E0F0FF', borderRadius: 8, padding: '14px 16px', fontSize: 12.5, color: '#003366', lineHeight: 1.55 }}>
            <b>Architecture note:</b> Limitador enforces, it does not meter. Billing events come from structured gateway access logs (Envoy ext-proc emits the full usage object per request) shipped via Vector to Loki, then compacted into the ClickHouse ledger. Loki retains raw logs 90 days for audit replay; Prometheus is used for ops, never for invoicing.
          </div>
        </div>
      </div>
    </div>
  );
}

export function ApiSurface({ D }) {
  return (
    <div data-screen-label="API reference">
      <PageH title="API surface" sub="The contract in and out of the platform — data plane, metering, management, webhooks, and the Kubernetes control plane underneath." />
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 14, marginBottom: 14 }}>
        <Card style={{ padding: '18px 20px' }}>
          <div style={{ fontFamily: DISPLAY, fontSize: 15, fontWeight: 700 }}>1 · Data plane (OpenAI-compatible, unchanged)</div>
          <div style={{ fontSize: 12.5, color: '#4d4d4d', marginTop: 6, lineHeight: 1.5 }}>
            <span style={{ fontFamily: MONO, fontSize: 12 }}>/v1/chat/completions · /v1/embeddings · /v1/models</span> — existing SDKs keep working. Billing context rides on response headers and the usage object.
          </div>
          <div style={{ marginTop: 12 }}>
            {D.apiHeaders.map(h => (
              <div key={h.name} style={{ display: 'flex', gap: 12, padding: '7px 0', borderTop: '1px solid #f5f5f5', fontSize: 12.5 }}>
                <div style={{ fontFamily: MONO, fontSize: 11.5, width: 250, flexShrink: 0, color: '#0066CC' }}>{h.name}</div>
                <div style={{ color: '#4d4d4d' }}>{h.desc}</div>
              </div>
            ))}
          </div>
          <Pre style={{ marginTop: 12 }}>{jsonUsage}</Pre>
        </Card>
        <Card style={{ padding: '18px 20px' }}>
          <div style={{ fontFamily: DISPLAY, fontSize: 15, fontWeight: 700 }}>2 · Metering ingest (internal)</div>
          <div style={{ fontSize: 12.5, color: '#4d4d4d', marginTop: 6, lineHeight: 1.5 }}>
            One event per inference call, emitted by the gateway. Idempotent on <span style={{ fontFamily: MONO, fontSize: 12 }}>request_id</span>; retries dedupe; late events reconcile rather than drop. <span style={{ fontFamily: MONO, fontSize: 12 }}>status</span> distinguishes client disconnects so disconnect-billing policy is explicit.
          </div>
          <Pre style={{ marginTop: 12 }}>{jsonEvent}</Pre>
        </Card>
      </div>
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 14 }}>
        <Card style={{ padding: '18px 20px' }}>
          <div style={{ fontFamily: DISPLAY, fontSize: 15, fontWeight: 700 }}>3 · Management API (tenant- and provider-facing)</div>
          <div style={{ marginTop: 10 }}>
            {D.apiMgmt.map(m => (
              <div key={m.path} style={{ display: 'flex', gap: 12, padding: '7px 0', borderTop: '1px solid #f5f5f5', fontSize: 12.5, alignItems: 'baseline' }}>
                <span style={{ fontFamily: MONO, fontSize: 10.5, fontWeight: 600, width: 44, flexShrink: 0, textAlign: 'center', padding: '2px 0', borderRadius: 4, background: PAL[m.c].bg, color: PAL[m.c].fg }}>{m.verb}</span>
                <div style={{ fontFamily: MONO, fontSize: 11.5, width: 290, flexShrink: 0 }}>{m.path}</div>
                <div style={{ color: '#4d4d4d' }}>{m.desc}</div>
              </div>
            ))}
          </div>
        </Card>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
          <Card style={{ padding: '18px 20px' }}>
            <div style={{ fontFamily: DISPLAY, fontSize: 15, fontWeight: 700 }}>4 · Webhooks out</div>
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, marginTop: 12 }}>
              {D.webhookTypes.map(w => (
                <span key={w} style={{ fontFamily: MONO, fontSize: 11.5, background: '#f5f5f5', borderRadius: 12, padding: '4px 12px', color: '#4d4d4d' }}>{w}</span>
              ))}
            </div>
            <Pre style={{ marginTop: 12 }}>{jsonWebhook}</Pre>
          </Card>
          <Card style={{ padding: '18px 20px' }}>
            <div style={{ fontFamily: DISPLAY, fontSize: 15, fontWeight: 700 }}>5 · Control plane (what it compiles to)</div>
            {D.controlPlane.map(c => (
              <div key={c.cr} style={{ display: 'flex', gap: 12, padding: '8px 0', borderTop: '1px solid #f5f5f5', fontSize: 12.5 }}>
                <div style={{ fontFamily: MONO, fontSize: 11.5, width: 210, flexShrink: 0, color: '#0066CC' }}>{c.cr}</div>
                <div style={{ color: '#4d4d4d', lineHeight: 1.45 }}>{c.desc}</div>
              </div>
            ))}
          </Card>
        </div>
      </div>
    </div>
  );
}
