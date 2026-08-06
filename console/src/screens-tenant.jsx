import React, { useState } from 'react';
import { Card, PageH, SecTitle, MiniLabel, Kpi, Badge, Btn, THead, TRow, Mono, Dim, Progress, Stacked, DISPLAY, MONO } from './ui.jsx';
import { LedgerTable, BudgetsPanel, KeysTable } from './screens-provider.jsx';
import { fmtMoney, fmtTokenCount, USAGE_LEGEND } from './utils.js';

function fmtRate(r) {
  if (!r) return '---';
  const n = parseFloat(r);
  return isNaN(n) ? '---' : '$' + n.toFixed(2);
}

export function TOverview({ th, ledger, goTo, usageDays, budgets }) {
  const tKpis = [
    { label: 'Prepaid balance', value: th.balance, delta: th.runway },
    { label: 'Spend · MTD', value: th.mtd || '---', delta: th.burn ? `Burn rate: ${th.burn}` : '' },
    { label: 'Plan', value: th.plan || '---', delta: th.mode || '' },
  ];
  return (
    <div data-screen-label="Tenant overview">
      <PageH title="Overview" sub={`${th.name} · ${th.plan} plan`} />
      <div style={{ display: 'flex', gap: 14, marginBottom: 14 }}>{tKpis.map(k => <Kpi key={k.label} {...k} />)}</div>
      <div style={{ display: 'grid', gridTemplateColumns: '1.6fr 1fr', gap: 14 }}>
        {usageDays && usageDays.length > 0
          ? <Stacked days={usageDays} legend={USAGE_LEGEND} title="Your tokens by type, daily" />
          : <Card style={{ padding: '18px 20px' }}><Dim>No usage data available yet.</Dim></Card>}
        <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
          <Card>
            <SecTitle right={<a href="#" onClick={e => { e.preventDefault(); goTo('t-budgets'); }} style={{ fontSize: 12 }}>Manage</a>}>Budgets</SecTitle>
            {budgets && budgets.length > 0 ? budgets.map(bp => (
              <div key={bp.name} style={{ padding: '8px 0', borderTop: '1px solid #f5f5f5' }}>
                <div style={{ display: 'flex', fontSize: 12.5 }}>
                  <div style={{ fontWeight: 600 }}>{bp.name}</div>
                  <Mono style={{ marginLeft: 'auto', fontSize: 11.5 }}>{fmtMoney(bp.current_spend)}</Mono>
                </div>
                {(bp.caps || []).map(cap => {
                  const spend = parseFloat(bp.current_spend || '0');
                  const capAmt = parseFloat(cap.amount || '1');
                  const pct = capAmt > 0 ? Math.min(100, Math.round((spend / capAmt) * 100)) : 0;
                  const color = pct > 90 ? '#B1380B' : pct > 70 ? '#F0AB00' : '#3D7317';
                  return <div key={cap.period} style={{ marginTop: 6 }}><Progress w={pct + '%'} color={color} h={6} /></div>;
                })}
              </div>
            )) : <Dim style={{ marginTop: 8 }}>No budgets configured.</Dim>}
          </Card>
          <Card style={{ flex: 1 }}>
            <SecTitle right={<a href="#" onClick={e => { e.preventDefault(); goTo('t-billing'); }} style={{ fontSize: 12 }}>Ledger</a>}>Recent activity</SecTitle>
            {ledger.length > 0 ? ledger.slice(0, 4).map((l, i) => (
              <div key={i} style={{ display: 'flex', gap: 8, padding: '7px 0', borderTop: '1px solid #f5f5f5', fontSize: 12, alignItems: 'center' }}>
                <Badge c={l.c} style={{ padding: '1px 8px', fontSize: 10.5 }}>{l.type}</Badge>
                <div style={{ flex: 1, color: '#6a6e73' }}>{l.date}</div>
                <Mono style={{ fontSize: 11.5, color: l.amtColor }}>{l.amt}</Mono>
              </div>
            )) : <Dim style={{ marginTop: 8 }}>No ledger entries yet.</Dim>}
          </Card>
        </div>
      </div>
    </div>
  );
}

export function TModels({ pricing, planName }) {
  const cols = '2fr .7fr .7fr .8fr .7fr';
  return (
    <div data-screen-label="Tenant model directory">
      <PageH title="Model directory" sub={`Your effective rates on the ${planName || '---'} plan. Rates per 1M tokens.`} />
      {pricing && pricing.length > 0 ? (
        <Card style={{ padding: 0, overflow: 'hidden' }}>
          <THead cols={cols} labels={['Model', 'Input', 'Output', 'Reasoning', 'Cached']} />
          {pricing.map((m, i) => (
            <TRow key={m.sku || m.model || i} cols={cols}>
              <div style={{ fontWeight: 600 }}>{m.sku || m.model || '---'}</div>
              <Mono>{fmtRate(m.rates?.prompt_tokens)}</Mono>
              <Mono>{fmtRate(m.rates?.completion_tokens)}</Mono>
              <Mono>{fmtRate(m.rates?.reasoning_tokens)}</Mono>
              <Mono>{fmtRate(m.rates?.cached_tokens)}</Mono>
            </TRow>
          ))}
        </Card>
      ) : (
        <Card style={{ padding: '18px 20px' }}>
          <Dim>No pricing data available. Contact your provider for rate details.</Dim>
        </Card>
      )}
      <div style={{ background: '#151515', borderRadius: 8, padding: '14px 18px', marginTop: 14, display: 'flex', alignItems: 'center', gap: 14 }}>
        <div style={{ fontFamily: MONO, fontSize: 12.5, color: '#d2d2d2' }}>OpenAI-compatible endpoint · existing SDKs work unchanged</div>
      </div>
    </div>
  );
}

export function TUsage({ usageDays, keyUsage }) {
  const cols = '1.3fr 1.8fr .9fr 1fr 1fr 1fr 1fr';
  return (
    <div data-screen-label="Tenant usage & reports">
      <PageH
        title="Usage & reports"
        sub={<span>Per-request metering, deduplicated — quote <span style={{ fontFamily: MONO, fontSize: 12 }}>x-maas-request-id</span> in any dispute.</span>}
        right={<><Btn small>Export CSV</Btn><Btn small>Export FOCUS 1.2</Btn></>}
      />
      {usageDays && usageDays.length > 0 ? (
        <div style={{ marginBottom: 14 }}><Stacked days={usageDays} legend={USAGE_LEGEND} title="Tokens by type, daily" height={140} /></div>
      ) : (
        <Card style={{ padding: '18px 20px', marginBottom: 14 }}><Dim>No usage data available yet.</Dim></Card>
      )}
      {keyUsage && keyUsage.length > 0 ? (
        <Card style={{ padding: 0, overflow: 'hidden' }}>
          <THead cols={cols} labels={['API key', 'Model', 'Requests', 'Input', 'Output', 'Reasoning', 'Billed']} />
          {keyUsage.map((u, i) => (
            <TRow key={u.key || i} cols={cols}>
              <div style={{ fontWeight: 600 }}>{u.key || '---'}</div><Dim>{u.model || '---'}</Dim>
              <Mono>{u.req || '---'}</Mono><Mono>{u.tin || '---'}</Mono><Mono>{u.tout || '---'}</Mono><Mono>{u.trsn || '---'}</Mono><Mono>{u.cost || '---'}</Mono>
            </TRow>
          ))}
        </Card>
      ) : (
        <Card style={{ padding: '18px 20px' }}><Dim>No per-key usage data available.</Dim></Card>
      )}
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

export function TBilling({ th, ledger, arOn, toggleAr, invoices }) {
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
          {invoices && invoices.length > 0 ? invoices.map((r, i) => (
            <div key={r.period || i} style={{ display: 'flex', gap: 8, padding: '8px 0', borderTop: '1px solid #f5f5f5', fontSize: 12.5, alignItems: 'center' }}>
              <div style={{ flex: 1 }}>{r.period || r.label || '---'}</div>
              <Mono>{fmtMoney(r.amount || r.totalAmount)}</Mono>
              <a href="#" onClick={e => e.preventDefault()} style={{ fontSize: 12 }}>PDF</a>
            </div>
          )) : (
            <Dim>No invoices yet.</Dim>
          )}
          <div style={{ fontSize: 12, color: '#6a6e73', marginTop: 12, borderTop: '1px solid #f0f0f0', paddingTop: 10 }}>
            Need invoicing instead of prepaid? <a href="#" onClick={e => e.preventDefault()}>Talk to sales about Enterprise (Net-30, PO).</a>
          </div>
        </Card>
      </div>
      <LedgerTable ledger={ledger} />
    </div>
  );
}

export function TBudgets({ apiBudgets }) {
  return (
    <div data-screen-label="Tenant budgets & alerts">
      <PageH title="Budgets & alerts" sub="Soft caps notify; hard caps throttle keys at 100%. Budgets reset monthly." />
      <BudgetsPanel apiBudgets={apiBudgets} addButtons />
    </div>
  );
}

export function TKeys({ keys, createKey, secret, dismiss }) {
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
        {keys && keys.length > 0 ? <KeysTable keys={keys} /> : <div style={{ padding: '12px 18px', fontSize: 12, color: '#6a6e73' }}>No API keys yet. Create one to get started.</div>}
      </Card>
    </div>
  );
}

/* ── Reservation helpers ── */

const RES_STATE_BADGE = { Active: 'green', Confirmed: 'blue', Cancelled: 'grey', Expired: 'gold' };

function fmtWindow(w) {
  if (!w || !w.start || !w.end) return '---';
  const fmt = d => new Date(d).toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric', timeZone: 'UTC' });
  return `${fmt(w.start)} – ${fmt(w.end)}`;
}

const inputStyle = { border: '1px solid #c7c7c7', borderRadius: 6, padding: '6px 10px', fontSize: 13, fontFamily: "'Red Hat Text',sans-serif", width: '100%', boxSizing: 'border-box' };

const EMPTY_FORM = { sku: '', count: '1', geo: '', class: 'Guaranteed', start: '', end: '', monthlyPrice: '' };

export function TReservations({ reservations, onCreateReservation, onCancelReservation }) {
  const [form, setForm] = useState({ ...EMPTY_FORM });
  const [showForm, setShowForm] = useState(false);

  const set = (k, v) => setForm(prev => ({ ...prev, [k]: v }));

  const handleCreate = () => {
    if (!form.sku || !form.start || !form.end || !form.monthlyPrice) return;
    onCreateReservation({
      tenant: '',
      sku: form.sku,
      count: parseInt(form.count, 10) || 1,
      geo: form.geo,
      class: form.class,
      start: new Date(form.start + 'T00:00:00Z').toISOString(),
      end: new Date(form.end + 'T23:59:59Z').toISOString(),
      monthlyPrice: form.monthlyPrice,
    });
    setForm({ ...EMPTY_FORM });
    setShowForm(false);
  };

  const handleCancel = name => {
    if (window.confirm(`Cancel reservation ${name}? Committed-use charges will be pro-rated to today.`)) {
      onCancelReservation(name);
    }
  };

  const cols = '1.5fr 1.2fr .5fr .7fr .8fr 1.4fr .8fr .6fr .6fr';
  return (
    <div data-screen-label="Tenant reservations">
      <PageH
        title="GPU reservations"
        sub="Reserved GPU capacity for your workloads. Guaranteed reservations are protected from preemption."
        right={<Btn primary small onClick={() => setShowForm(!showForm)}>{showForm ? 'Cancel' : 'New reservation'}</Btn>}
      />

      {showForm && (
        <Card style={{ marginBottom: 14, padding: '18px 20px' }}>
          <SecTitle style={{ marginBottom: 14 }}>New reservation</SecTitle>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 12 }}>
            <div><MiniLabel>SKU</MiniLabel><input style={inputStyle} value={form.sku} onChange={e => set('sku', e.target.value)} placeholder="nvidia-a100-80gb" /></div>
            <div><MiniLabel>Count</MiniLabel><input type="number" min="1" style={inputStyle} value={form.count} onChange={e => set('count', e.target.value)} /></div>
            <div><MiniLabel>Geography</MiniLabel><input style={inputStyle} value={form.geo} onChange={e => set('geo', e.target.value)} placeholder="us-east" /></div>
            <div><MiniLabel>Class</MiniLabel><select style={inputStyle} value={form.class} onChange={e => set('class', e.target.value)}><option>Guaranteed</option><option>Preemptible</option></select></div>
            <div><MiniLabel>Start date</MiniLabel><input type="date" style={inputStyle} value={form.start} onChange={e => set('start', e.target.value)} /></div>
            <div><MiniLabel>End date</MiniLabel><input type="date" style={inputStyle} value={form.end} onChange={e => set('end', e.target.value)} /></div>
            <div><MiniLabel>Monthly price</MiniLabel><input style={{ ...inputStyle, fontFamily: MONO }} value={form.monthlyPrice} onChange={e => set('monthlyPrice', e.target.value)} placeholder="14400.00" /></div>
            <div style={{ display: 'flex', alignItems: 'flex-end' }}><Btn primary onClick={handleCreate}>Create reservation</Btn></div>
          </div>
        </Card>
      )}

      {reservations && reservations.length > 0 ? (
        <Card style={{ padding: 0, overflow: 'hidden' }}>
          <THead cols={cols} labels={['Name', 'SKU', 'GPUs', 'Geo', 'Class', 'Window', 'Price/mo', 'State', '']} />
          {reservations.map(r => (
            <TRow key={r.name} cols={cols}>
              <Mono style={{ fontSize: 11.5 }}>{r.name}</Mono>
              <div>{r.sku}</div>
              <Mono>{r.count}</Mono>
              <div>{r.geo}</div>
              <div>{r.class}</div>
              <Dim style={{ fontSize: 12 }}>{fmtWindow(r.window)}</Dim>
              <Mono>{fmtMoney(r.monthlyPrice)}</Mono>
              <Badge c={RES_STATE_BADGE[r.state] || 'grey'}>{r.state}</Badge>
              <div>
                {(r.state === 'Active' || r.state === 'Confirmed') && (
                  <Btn danger small onClick={() => handleCancel(r.name)}>Cancel</Btn>
                )}
              </div>
            </TRow>
          ))}
        </Card>
      ) : (
        <Card style={{ padding: '18px 20px' }}><Dim>No GPU reservations yet. Create one to reserve capacity for your workloads.</Dim></Card>
      )}
    </div>
  );
}

