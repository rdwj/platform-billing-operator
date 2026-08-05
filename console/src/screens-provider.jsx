import React from 'react';
import { Card, PageH, SecTitle, MiniLabel, Kpi, Badge, Btn, THead, TRow, Mono, Dim, Progress, Bars, Stacked, Pre, DISPLAY, MONO, PAL } from './ui.jsx';
import { planYaml, jsonUsage, jsonEvent, jsonWebhook } from './samples.js';
import { fmtMoney, fmtTokenCount, USAGE_LEGEND } from './utils.js';

/* ── Static reference data (inlined from former data.js) ── */

const ENF_LAYERS = [
  { layer: 'AuthPolicy', desc: 'Authorino validates the API key, resolves tenant, tier, model + geo scopes into auth.identity claims.' },
  { layer: 'RateLimitPolicy', desc: 'Request-rate and concurrency guardrails per tenant — protects the fleet from bursts.' },
  { layer: 'TokenRateLimitPolicy', desc: 'Token budgets per window; gateway reports actual usage as hits_addend after the response.' },
  { layer: 'Balance allowance', desc: 'Custom controller converts prepaid balance into short-window token grants; depletion freezes keys.' }
];

const API_HEADERS = [
  { name: 'x-maas-request-id', desc: 'Join key into the usage ledger — quote it in billing disputes.' },
  { name: 'x-ratelimit-remaining-tokens', desc: 'Tokens left in the current window (plan-level).' },
  { name: 'x-ratelimit-remaining-requests', desc: 'Requests left in the current window.' },
  { name: 'x-maas-balance-remaining', desc: 'Prepaid balance after this call · eventual (≤60s).' },
  { name: 'x-maas-price-version', desc: 'Price book applied to this request (e.g. v2).' }
];

const API_MGMT = [
  ['GET', '/v1/usage?group_by=model,key&bucket=1h', 'Query the ledger — powers Usage explorer + tenant dashboards.'],
  ['GET', '/v1/balance', 'Prepaid balance, runway, reload config.'],
  ['POST', '/v1/credits', 'Grant credit (promo, SLA, goodwill) — audited.'],
  ['PUT', '/v1/reload-policy', 'Threshold, amount, cap, payment method.'],
  ['GET', '/v1/pricing?model=…&geo=…', 'Effective price book for a tenant (volume tier applied).'],
  ['POST', '/v1/keys', 'Create key with model/geo scopes + per-key limits.'],
  ['PUT', '/v1/limits/{keyId}', 'Compiles to Kuadrant policy on the gateway.'],
  ['GET', '/v1/invoices/{period}', 'Line items traceable to ledger request_ids.'],
  ['GET', '/v1/reports/chargeback?format=focus', 'FOCUS 1.2 / CSV export per cost center.']
].map(([verb, path, desc]) => ({ verb, path, desc, c: verb === 'GET' ? 'blue' : verb === 'POST' ? 'green' : 'gold' }));

const WEBHOOK_TYPES = ['balance.low', 'balance.depleted', 'limit.exceeded', 'budget.threshold', 'invoice.finalized', 'payment.failed', 'price.changed', 'key.rotated', 'usage.anomaly'];

const CONTROL_PLANE = [
  { cr: 'AuthPolicy', desc: 'API key → identity claims (tenant, tier, scopes) via Authorino.' },
  { cr: 'TokenRateLimitPolicy', desc: 'Token windows per tier/tenant — Limitador counters, Redis-backed cross-cluster.' },
  { cr: 'RateLimitPolicy', desc: 'Request-rate + concurrency guardrails.' },
  { cr: 'LLMInferenceService', desc: 'Model serving (KServe / llm-d) per geo — the SKU’s supply side.' },
  { cr: 'BalanceAllowance (custom)', desc: 'Prepaid balance → rolling short-window token grants; the piece RHOAI does not ship today.' }
];

/* ── Overview helpers ── */

function buildOverviewKpis(ov, apiTenants) {
  const delta = ov.revenueKpis.deltaPct;
  const tokenTotal = ov.tokenMix.reduce((s, t) => s + t.total, 0);
  const tenantCount = apiTenants ? apiTenants.length : 0;
  const alertStates = ov.alerts.tenantStates || {};
  const lowBal = (alertStates['Warning'] || 0);
  const frozenCount = (alertStates['Frozen'] || 0);
  return [
    { label: 'Usage revenue · MTD', value: fmtMoney(ov.revenueKpis.mtdRevenue), delta: `${parseFloat(delta) >= 0 ? '+' : ''}${delta}% vs prior month`, deltaColor: parseFloat(delta) >= 0 ? '#204D00' : '#B1380B' },
    { label: 'Tokens served · MTD', value: fmtTokenCount(tokenTotal), delta: ov.tokenMix.map(t => `${fmtTokenCount(t.total)} ${t.quantityType.replace('_tokens', '')}`).join(' · '), deltaColor: '#6a6e73' },
    { label: 'Active tenants', value: String(tenantCount), delta: `${lowBal} low balance · ${frozenCount} frozen`, deltaColor: lowBal > 0 ? '#73480B' : '#6a6e73' },
    { label: 'Trailing 30d revenue', value: fmtMoney(ov.revenueKpis.trailing30d), delta: `Prior month: ${fmtMoney(ov.revenueKpis.priorMonth)}`, deltaColor: '#6a6e73' }
  ];
}

function buildOverviewWeeks(weeks) {
  if (!weeks || weeks.length === 0) return [];
  const maxRev = Math.max(1, ...weeks.map(w => parseFloat(w.revenue)));
  return weeks.map(w => {
    const rev = parseFloat(w.revenue);
    const dt = new Date(w.weekStart);
    const label = dt.toLocaleDateString('en-US', { month: 'short', day: 'numeric', timeZone: 'UTC' });
    return { h: Math.max(2, Math.round((rev / maxRev) * 100)) + 'px', label, tip: `${label}: ${fmtMoney(w.revenue)}` };
  });
}

function buildTokenMix(mix) {
  const COLORS = { prompt_tokens: '#0066CC', completion_tokens: '#009596', reasoning_tokens: '#5752D1', cached_tokens: '#F0AB00' };
  const LABELS = { prompt_tokens: 'Input', completion_tokens: 'Output', reasoning_tokens: 'Reasoning', cached_tokens: 'Cached reads' };
  const total = mix.reduce((s, t) => s + t.total, 0) || 1;
  return mix.map(t => ({
    label: LABELS[t.quantityType] || t.quantityType,
    amt: fmtTokenCount(t.total),
    rev: '',
    w: Math.max(1, Math.round((t.total / total) * 100)) + '%',
    color: COLORS[t.quantityType] || '#8a8d90',
    tip: `${LABELS[t.quantityType] || t.quantityType}: ${fmtTokenCount(t.total)} tokens`,
  }));
}

function buildOverviewAlerts(ov) {
  const alerts = [];
  const states = ov.alerts.tenantStates || {};
  if (states['Frozen'] > 0) alerts.push({ color: '#B1380B', text: `${states['Frozen']} tenant(s) frozen — keys suspended.` });
  if (states['Dunning'] > 0) alerts.push({ color: '#B1380B', text: `${states['Dunning']} tenant(s) in dunning — payment collection in progress.` });
  if (states['Grace'] > 0) alerts.push({ color: '#F0AB00', text: `${states['Grace']} tenant(s) in grace period — balance depleted, keys still active.` });
  if (states['Warning'] > 0) alerts.push({ color: '#F0AB00', text: `${states['Warning']} tenant(s) in warning — low balance detected.` });
  if (ov.alerts.firedBudgets > 0) alerts.push({ color: '#F0AB00', text: `${ov.alerts.firedBudgets} budget threshold(s) fired this period.` });
  if (alerts.length === 0) alerts.push({ color: '#3D7317', text: 'No active alerts — all tenants healthy.' });
  return alerts;
}

/* ── Overview ── */

export function Overview({ apiTenants, apiOverview, onTenant, goMetering }) {
  const ov = apiOverview;
  if (!ov) {
    return (
      <div data-screen-label="Revenue overview">
        <PageH title="Revenue overview" sub="Month-to-date · all geos · USD-settled" />
        <Card style={{ padding: '18px 20px' }}><Dim>Loading overview data...</Dim></Card>
      </div>
    );
  }
  const kpis = buildOverviewKpis(ov, apiTenants);
  const weeks = buildOverviewWeeks(ov.trailingWeeks);
  const tokenMix = buildTokenMix(ov.tokenMix);
  const topModels = ov.topModels
    ? ov.topModels.map(m => ({ name: m.sku, rev: fmtMoney(m.amount), tok: '' }))
    : [];
  const alerts = buildOverviewAlerts(ov);
  const pipeColor = ov.meteringStatus.driftExceeded > 0 || ov.meteringStatus.dlqDepth > 5 ? '#B1380B' : '#3D7317';
  const pipeSummary = ov.meteringStatus.driftExceeded > 0
    ? `Drift exceeded on ${ov.meteringStatus.driftExceeded} report(s) · DLQ depth: ${ov.meteringStatus.dlqDepth}`
    : `Healthy — DLQ depth: ${ov.meteringStatus.dlqDepth}`;

  const topTenants = ov.topTenants
    ? ov.topTenants.map(t => ({ tenant_id: t.tenantId, name: t.displayName, mtd_spend: t.amount }))
    : apiTenants
      ? apiTenants.map(t => ({ ...t, tid: t.tenant_id, mtdN: parseFloat(t.mtd_spend || '0') })).sort((a, b) => b.mtdN - a.mtdN).slice(0, 5)
      : [];

  return (
    <div data-screen-label="Revenue overview">
      <PageH title="Revenue overview" sub="Month-to-date · all geos · USD-settled" />
      <div style={{ display: 'flex', gap: 14, marginBottom: 14 }}>{kpis.map(k => <Kpi key={k.label} {...k} />)}</div>
      <div style={{ display: 'grid', gridTemplateColumns: '1.6fr 1fr', gap: 14, marginBottom: 14 }}>
        <Card style={{ padding: '18px 20px' }}>
          <SecTitle right={<Dim>weekly, settled</Dim>}>Usage revenue, trailing 12 weeks</SecTitle>
          <Bars weeks={weeks} />
        </Card>
        <Card style={{ padding: '18px 20px', display: 'flex', flexDirection: 'column' }}>
          <SecTitle>Token mix, MTD</SecTitle>
          <div style={{ display: 'flex', height: 14, borderRadius: 7, overflow: 'hidden', marginTop: 16 }}>
            {tokenMix.map(t => <div key={t.label} title={t.tip} style={{ width: t.w, background: t.color }} />)}
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginTop: 16 }}>
            {tokenMix.map(t => (
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
          {topTenants.map(t => (
            <div key={t.tid || t.tenant_id} style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '7px 0', borderTop: '1px solid #f5f5f5', fontSize: 13 }}>
              <a href="#" onClick={e => { e.preventDefault(); onTenant(t); }} style={{ flex: 1, fontWeight: 500 }}>{t.name}</a>
              <Mono>{t.mtd || fmtMoney(t.mtd_spend)}</Mono>
            </div>
          ))}
          {topTenants.length === 0 && <Dim>No tenant data available.</Dim>}
        </Card>
        <Card>
          <SecTitle style={{ marginBottom: 10 }}>Top models (MTD)</SecTitle>
          {topModels.map(m => (
            <div key={m.name} style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '7px 0', borderTop: '1px solid #f5f5f5', fontSize: 13 }}>
              <div style={{ flex: 1, fontWeight: 500 }}>{m.name}</div>
              <Mono>{m.rev}</Mono>
              {m.tok && <Dim style={{ width: 56, textAlign: 'right' }}>{m.tok}</Dim>}
            </div>
          ))}
          {topModels.length === 0 && <Dim>No model data available.</Dim>}
        </Card>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
          <Card style={{ padding: '14px 18px', borderLeft: `4px solid ${pipeColor}` }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
              <div style={{ width: 9, height: 9, borderRadius: '50%', background: pipeColor }} />
              <div style={{ fontFamily: DISPLAY, fontSize: 14, fontWeight: 600 }}>Metering pipeline</div>
              <a href="#" onClick={e => { e.preventDefault(); goMetering(); }} style={{ marginLeft: 'auto', fontSize: 12 }}>Detail</a>
            </div>
            <div style={{ fontSize: 12, color: '#4d4d4d', marginTop: 6 }}>{pipeSummary}</div>
          </Card>
          <Card style={{ padding: '14px 18px', flex: 1 }}>
            <div style={{ fontFamily: DISPLAY, fontSize: 14, fontWeight: 600, marginBottom: 8 }}>Needs attention</div>
            {alerts.map((a, i) => (
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

/* ── Models & pricing ── */

function fmtRate(r) {
  if (!r) return '---';
  const n = parseFloat(r);
  return isNaN(n) ? '---' : '$' + n.toFixed(2);
}

function mapPriceBookToModels(pb) {
  return (pb.skus || []).map(s => ({
    name: s.sku,
    geo: s.geo || '---',
    pin: fmtRate(s.rates?.prompt_tokens),
    pout: fmtRate(s.rates?.completion_tokens),
    prsn: fmtRate(s.rates?.reasoning_tokens),
    pcache: fmtRate(s.rates?.cached_tokens),
    ver: pb.name,
  }));
}

function mapPriceBookGeoRates(pb) {
  const byModel = {};
  for (const s of (pb.skus || [])) {
    if (!s.geo) continue;
    const key = s.geo;
    if (!byModel[key]) byModel[key] = { geo: key };
    byModel[key].pin = fmtRate(s.rates?.prompt_tokens);
    byModel[key].pout = fmtRate(s.rates?.completion_tokens);
    byModel[key].prsn = fmtRate(s.rates?.reasoning_tokens);
    byModel[key].pcache = fmtRate(s.rates?.cached_tokens);
  }
  return Object.values(byModel);
}

export function Models({ apiPriceBooks }) {
  const pb = apiPriceBooks && apiPriceBooks.length > 0 ? apiPriceBooks[0] : null;
  if (!pb) {
    return (
      <div data-screen-label="Models & pricing">
        <PageH title="Model catalog & pricing" sub="A SKU is model × geo × price version. Rates are per 1M tokens." right={<Btn primary>Add model</Btn>} />
        <Card style={{ padding: '18px 20px' }}><Dim>No price books configured. Create a PriceBook CR to define model pricing.</Dim></Card>
      </div>
    );
  }
  const models = mapPriceBookToModels(pb);
  const geoRates = mapPriceBookGeoRates(pb);
  const volTiers = pb.volumeTiers ? pb.volumeTiers.map(vt => ({
    band: `≥ ${fmtTokens(vt.thresholdTokens)} tokens / mo`,
    disc: `-${vt.discountPct}%`,
  })) : [];
  const committed = pb.committedUse ? pb.committedUse.map(cu => ({
    band: `Reserved ${cu.tokensPerSecond} TPS (${cu.name})`,
    price: fmtMoney(cu.monthlyPrice) + ' /mo',
  })) : [];
  const pbName = pb.name;
  const pbEffective = pb.effectiveFrom?.slice(0, 10) || '---';
  const pbState = pb.state || 'Active';

  const cols = '2fr .7fr .7fr .8fr .7fr .6fr';
  const headers = ['SKU', 'Input', 'Output', 'Reasoning', 'Cached', 'Price bk'];
  return (
    <div data-screen-label="Models & pricing">
      <PageH title="Model catalog & pricing" sub="A SKU is model × geo × price version. Rates are per 1M tokens." right={<Btn primary>Add model</Btn>} />
      <Card style={{ padding: 0, overflow: 'hidden', marginBottom: 16 }}>
        <THead cols={cols} labels={headers} />
        {models.map((m, i) => (
          <TRow key={m.name + (m.geo || '') + i} cols={cols}>
            <div style={{ fontWeight: 600 }}>{m.name}{m.geo && m.geo !== '---' ? <Dim style={{ fontWeight: 400, marginLeft: 6 }}>({m.geo})</Dim> : null}</div>
            <Mono>{m.pin}</Mono><Mono>{m.pout}</Mono><Mono>{m.prsn}</Mono><Mono>{m.pcache}</Mono>
            <Dim>{m.ver}</Dim>
          </TRow>
        ))}
      </Card>
      <Card style={{ padding: 20 }}>
        <div style={{ display: 'flex', alignItems: 'baseline', gap: 10 }}>
          <div style={{ fontFamily: DISPLAY, fontSize: 16, fontWeight: 700 }}>Price book — {pbName}</div>
          <Badge c={pbState === 'Active' ? 'blue' : 'grey'}>{pbState} · effective {pbEffective}</Badge>
        </div>
        <div style={{ display: 'grid', gridTemplateColumns: geoRates.length > 0 ? '1.2fr 1fr 1fr' : '1fr 1fr', gap: 20, marginTop: 16 }}>
          {geoRates.length > 0 && (
            <div>
              <MiniLabel>Geo variants</MiniLabel>
              {geoRates.map(g => (
                <div key={g.geo} style={{ border: '1px solid #e8e8e8', borderRadius: 6, padding: '10px 14px', margin: '8px 0' }}>
                  <div style={{ fontSize: 13, fontWeight: 600 }}>{g.geo}</div>
                  <div style={{ display: 'flex', gap: 14, marginTop: 6, fontFamily: MONO, fontSize: 12, color: '#4d4d4d' }}>
                    <div>in {g.pin}</div><div>out {g.pout}</div><div>rsn {g.prsn}</div><div>cached {g.pcache}</div>
                  </div>
                </div>
              ))}
            </div>
          )}
          <div>
            {volTiers.length > 0 && <>
              <MiniLabel>Volume discounts (monthly)</MiniLabel>
              {volTiers.map(v => (
                <div key={v.band} style={{ display: 'flex', justifyContent: 'space-between', padding: '8px 2px', borderBottom: '1px solid #f0f0f0', fontSize: 13 }}>
                  <div>{v.band}</div><div style={{ fontWeight: 600 }}>{v.disc}</div>
                </div>
              ))}
            </>}
            {committed.length > 0 && <>
              <div style={{ margin: '16px 0 8px' }}><MiniLabel>Committed use</MiniLabel></div>
              {committed.map(c => (
                <div key={c.band} style={{ display: 'flex', justifyContent: 'space-between', padding: '8px 2px', borderBottom: '1px solid #f0f0f0', fontSize: 13 }}>
                  <div>{c.band}</div><Mono style={{ fontWeight: 600 }}>{c.price}</Mono>
                </div>
              ))}
            </>}
            {volTiers.length === 0 && committed.length === 0 && <Dim>No volume discounts or committed use tiers configured.</Dim>}
          </div>
          <div>
            <MiniLabel>PriceBook details</MiniLabel>
            <div style={{ padding: '8px 0', fontSize: 12.5, lineHeight: 1.5, color: '#4d4d4d' }}>
              <div><b>Currency:</b> {pb.currency}</div>
              <div><b>SKUs:</b> {pb.skuCount}</div>
              <div><b>Active tenants:</b> {pb.activeTenants}</div>
              {pb.providerFaultPolicy && <div><b>Provider fault:</b> {pb.providerFaultPolicy}</div>}
              {pb.clientDisconnectPolicy && <div><b>Client disconnect:</b> {pb.clientDisconnectPolicy}</div>}
            </div>
          </div>
        </div>
      </Card>
    </div>
  );
}

/* ── Tenants ── */

function badgeColor(status) {
  return { Active: 'green', Warning: 'gold', Grace: 'gold', Dunning: 'red', Frozen: 'red', Suspended: 'red', Pending: 'grey', Closed: 'grey' }[status] || 'grey';
}

function mapApiTenant(t) {
  const bal = t.balance ? parseFloat(t.balance) : null;
  return {
    tid: t.tenant_id, name: t.name, plan: t.plan, mode: t.billing_mode || '---',
    balance: bal !== null ? fmtMoney(t.balance) : '---',
    balColor: bal !== null && bal < 1300 ? '#B1380B' : '#151515',
    reload: t.reload_enabled ? 'Enabled' : 'Off',
    burn: t.burn_per_day ? fmtMoney(t.burn_per_day) + '/d' : '---',
    mtd: fmtMoney(t.mtd_spend), status: t.status || 'Pending', st: badgeColor(t.status),
    tenant_id: t.tenant_id,
    ...t,
  };
}

export function Tenants({ apiTenants, onTenant }) {
  if (!apiTenants) {
    return (
      <div data-screen-label="Tenants">
        <PageH title="Tenants" sub="Loading tenant data..." />
        <Card style={{ padding: '18px 20px' }}><Dim>Loading...</Dim></Card>
      </div>
    );
  }
  const tenants = apiTenants.map(mapApiTenant);
  const activeCount = tenants.filter(t => t.status === 'Active').length;
  const lowBal = tenants.filter(t => t.st === 'gold' || (t.balColor === '#B1380B')).length;
  const frozenCount = tenants.filter(t => t.status === 'Frozen').length;
  const sub = `${activeCount} active${lowBal ? ` · ${lowBal} low balance` : ''}${frozenCount ? ` · ${frozenCount} frozen` : ''} · click a tenant to drill in`;
  const cols = '1.8fr .9fr 1fr 1fr 1.6fr .9fr 1fr .9fr';
  return (
    <div data-screen-label="Tenants">
      <PageH title="Tenants" sub={sub} right={<Btn primary>Invite tenant</Btn>} />
      <Card style={{ padding: 0, overflow: 'hidden' }}>
        <THead cols={cols} labels={['Tenant', 'Plan', 'Billing mode', 'Balance', 'Auto-reload', 'Burn / day', 'MTD spend', 'Status']} />
        {tenants.map(t => (
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

/* ── Tenant detail ── */

export function TenantDetail({ td, tab, setTab, ledger, apiBudgets, apiKeys, apiExport, back, grantCredit, grantLabel, toggleFreeze, freezeLabel, goPlans }) {
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

      {tab === 'budgets' && <BudgetsPanel apiBudgets={apiBudgets} />}

      {tab === 'keys' && <>
        <Card style={{ padding: 0, overflow: 'hidden' }}>
          <div style={{ display: 'flex', alignItems: 'center', padding: '14px 18px' }}>
            <div style={{ fontFamily: DISPLAY, fontSize: 15, fontWeight: 600 }}>API keys</div>
            <div style={{ marginLeft: 'auto' }}><Btn primary small>Create key</Btn></div>
          </div>
          {apiKeys && apiKeys.keys ? <ApiKeysTable keys={apiKeys.keys} /> : (
            <div style={{ padding: '12px 18px', fontSize: 12, color: '#6a6e73' }}>No API keys found for this tenant.</div>
          )}
        </Card>
        <div style={{ background: '#E0F0FF', borderRadius: 8, padding: '12px 16px', fontSize: 12.5, color: '#003366', marginTop: 12 }}>
          Keys map to Authorino identities; scopes and per-key limits compile to AuthPolicy claims consumed by TokenRateLimitPolicy predicates.
        </div>
      </>}

      {tab === 'chargeback' && <>
        <Card style={{ padding: 0, overflow: 'hidden' }}>
          <div style={{ display: 'flex', alignItems: 'center', padding: '14px 18px' }}>
            <div style={{ fontFamily: DISPLAY, fontSize: 15, fontWeight: 600 }}>Chargeback / showback — MTD</div>
            <div style={{ marginLeft: 'auto', display: 'flex', gap: 8 }}>
              <Btn small>Export CSV</Btn><Btn small>Export FOCUS 1.2</Btn><Btn small>Schedule…</Btn>
            </div>
          </div>
          {apiExport && apiExport.rows ? (
            <>
              <THead cols="2fr 1fr 1fr 1fr .8fr" labels={['SKU', 'Input tok', 'Output tok', 'Billed cost', 'Share']} />
              {(() => {
                const total = apiExport.rows.reduce((s, x) => s + parseFloat(x.billed_cost || '0'), 0);
                return apiExport.rows.map((r, i) => {
                  const share = total > 0 ? Math.round((parseFloat(r.billed_cost || '0') / total) * 100) : 0;
                  return (
                    <TRow key={i} cols="2fr 1fr 1fr 1fr .8fr">
                      <div style={{ fontWeight: 600 }}>{r.sku_id || r.service_name || '---'}</div>
                      <Mono>{r.pricing_quantity || '---'}</Mono><Mono>{r.consumed_quantity || '---'}</Mono>
                      <Mono>{fmtMoney(r.billed_cost)}</Mono>
                      <div><Progress w={share + '%'} h={7} /></div>
                    </TRow>
                  );
                });
              })()}
            </>
          ) : (
            <div style={{ padding: '12px 18px', fontSize: 12, color: '#6a6e73' }}>No chargeback data available for this tenant.</div>
          )}
        </Card>
        <Dim style={{ marginTop: 10 }}>FOCUS export maps token usage to the FinOps Open Cost & Usage Specification so tenants can ingest into Cloudability, CloudHealth, etc. Attribution key: API key → project → cost center (set per key).</Dim>
      </>}
    </div>
  );
}

/* ── Ledger table ── */

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

/* ── Keys tables ── */

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

function ApiKeysTable({ keys }) {
  const cols = '1.5fr 1fr 1fr 1fr .8fr';
  return (<>
    <THead cols={cols} labels={['Name', 'Source', 'Created', 'Status', '']} />
    {keys.map(k => (
      <TRow key={k.key_name} cols={cols}>
        <div style={{ fontWeight: 600 }}>{k.key_name}</div>
        <Dim>{k.source || '---'}</Dim>
        <Dim>{k.created_at ? new Date(k.created_at).toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' }) : '---'}</Dim>
        <div><Badge c={k.enabled ? 'green' : 'red'}>{k.enabled ? 'Active' : 'Disabled'}</Badge></div>
        <div style={{ display: 'flex', gap: 10, fontSize: 12 }}>
          <a href="#" onClick={e => e.preventDefault()}>Rotate</a>
          <a href="#" onClick={e => e.preventDefault()} style={{ color: '#B1380B' }}>Revoke</a>
        </div>
      </TRow>
    ))}
  </>);
}

/* ── Budgets panel (shared by TenantDetail and TBudgets) ── */

export function BudgetsPanel({ addButtons, apiBudgets }) {
  const hasBudgets = apiBudgets && apiBudgets.budgets && apiBudgets.budgets.length > 0;
  if (hasBudgets) {
    const budgets = apiBudgets.budgets;
    const alertRules = [];
    for (const bp of budgets) {
      for (const cap of (bp.caps || [])) {
        for (const th of (cap.thresholds || [])) {
          alertRules.push({
            when: `${cap.period} spend hits ${th.pct}% of ${fmtMoney(cap.amount)}`,
            then: th.action === 'Notify' ? 'Send webhook alert to configured endpoints' : th.action === 'Freeze' ? 'Freeze tenant API keys' : `${th.action} enforcement`,
            fired: (bp.fired_thresholds || []).some(ft => ft.pct === th.pct),
          });
        }
      }
    }
    return (
      <div style={{ display: 'grid', gridTemplateColumns: '1.3fr 1fr', gap: 14 }}>
        <Card style={{ padding: '18px 20px' }}>
          <div style={{ display: 'flex', alignItems: 'center', marginBottom: 14 }}>
            <div style={{ fontFamily: DISPLAY, fontSize: 15, fontWeight: 600 }}>Budgets</div>
            {addButtons && <div style={{ marginLeft: 'auto' }}><Btn small>Add budget</Btn></div>}
          </div>
          {budgets.flatMap(bp => (bp.caps || []).map(cap => {
            const spend = parseFloat(bp.current_spend || '0');
            const capAmt = parseFloat(cap.amount || '1');
            const pct = capAmt > 0 ? Math.min(100, Math.round((spend / capAmt) * 100)) : 0;
            const color = pct > 90 ? '#B1380B' : pct > 70 ? '#F0AB00' : '#3D7317';
            return (
              <div key={bp.name + cap.period} style={{ padding: '10px 0', borderTop: '1px solid #f5f5f5' }}>
                <div style={{ display: 'flex', fontSize: 13 }}>
                  <div style={{ fontWeight: 600 }}>{bp.name} ({cap.period})</div>
                  <Mono style={{ marginLeft: 'auto' }}>{fmtMoney(bp.current_spend)} / {fmtMoney(cap.amount)}</Mono>
                </div>
                <div style={{ marginTop: 8 }}><Progress w={pct + '%'} color={color} /></div>
                <div style={{ fontSize: 11, color: '#6a6e73', marginTop: 5 }}>
                  {pct}% utilized · {cap.thresholds.length} threshold{cap.thresholds.length !== 1 ? 's' : ''} configured
                </div>
              </div>
            );
          }))}
        </Card>
        <Card style={{ padding: '18px 20px' }}>
          <div style={{ fontFamily: DISPLAY, fontSize: 15, fontWeight: 600, marginBottom: 8 }}>Alert rules</div>
          {alertRules.length > 0 ? alertRules.map(r => (
            <div key={r.when} style={{ display: 'flex', gap: 10, padding: '9px 0', borderTop: '1px solid #f5f5f5', fontSize: 12.5, lineHeight: 1.45 }}>
              <div style={{ flex: 1 }}><b>{r.when}</b><div style={{ color: '#6a6e73', marginTop: 2 }}>{r.then}</div></div>
              <Badge c={r.fired ? 'gold' : 'green'} style={{ height: 'fit-content' }}>{r.fired ? 'Fired' : 'On'}</Badge>
            </div>
          )) : <Dim>No budget thresholds configured</Dim>}
          <div style={{ marginTop: 12 }}><Btn small>Add rule</Btn></div>
        </Card>
      </div>
    );
  }
  return (
    <div style={{ display: 'grid', gridTemplateColumns: '1.3fr 1fr', gap: 14 }}>
      <Card style={{ padding: '18px 20px' }}>
        <div style={{ display: 'flex', alignItems: 'center', marginBottom: 14 }}>
          <div style={{ fontFamily: DISPLAY, fontSize: 15, fontWeight: 600 }}>Budgets</div>
          {addButtons && <div style={{ marginLeft: 'auto' }}><Btn small>Add budget</Btn></div>}
        </div>
        <Dim>No budgets configured. Create a BudgetPolicy CR to set spending limits.</Dim>
      </Card>
      <Card style={{ padding: '18px 20px' }}>
        <div style={{ fontFamily: DISPLAY, fontSize: 15, fontWeight: 600, marginBottom: 8 }}>Alert rules</div>
        <Dim>No alert rules configured.</Dim>
        <div style={{ marginTop: 12 }}><Btn small>Add rule</Btn></div>
      </Card>
    </div>
  );
}

/* ── Plans & rate limits ── */

function fmtTokens(n) {
  if (n == null) return 'Custom';
  if (n >= 1e9) return (n / 1e9).toFixed(n % 1e9 === 0 ? 0 : 1) + 'B';
  if (n >= 1e6) return (n / 1e6).toFixed(n % 1e6 === 0 ? 0 : 1) + 'M';
  if (n >= 1e3) return (n / 1e3).toFixed(n % 1e3 === 0 ? 0 : 1) + 'k';
  return String(n);
}

function mapApiPlan(p) {
  const lim = p.limits || {};
  return {
    name: p.name,
    slug: p.name.toLowerCase(),
    rpm: lim.requestsPerMinute != null ? String(lim.requestsPerMinute) : 'Custom',
    tpm: fmtTokens(lim.tokensPerMinute),
    tpmN: lim.tokensPerMinute || 0,
    day: fmtTokens(lim.tokensPerDay),
    dayN: lim.tokensPerDay || 0,
    conc: lim.maxConcurrency != null ? String(lim.maxConcurrency) : 'Custom',
    tenantsN: p.tenantCount || 0,
    models: p.modelScopes && p.modelScopes.length > 0 ? p.modelScopes.join(', ') : 'All models',
    queue: p.priceBookRef ? `PriceBook: ${p.priceBookRef}` : '',
    compiledPolicies: p.compiledPolicies || [],
  };
}

export function Plans({ plan, setPlan, apiPlans }) {
  if (!apiPlans) {
    return (
      <div data-screen-label="Plans & rate limits">
        <PageH title="Plans & rate limits" sub="Loading plans..." />
        <Card style={{ padding: '18px 20px' }}><Dim>Loading...</Dim></Card>
      </div>
    );
  }
  const plans = apiPlans.map(mapApiPlan);
  const sel = plans.find(p => p.name === plan) || plans[0];
  if (!sel) return <div data-screen-label="Plans & rate limits"><PageH title="Plans & rate limits" sub="No plans configured." /></div>;
  const allUpToDate = sel.compiledPolicies && sel.compiledPolicies.length > 0 && sel.compiledPolicies.every(cp => cp.upToDate);
  return (
    <div data-screen-label="Plans & rate limits">
      <PageH title="Plans & rate limits" sub="Plans compile to Kuadrant policy CRs on the MaaS gateway. Select a plan to inspect the generated policy." />
      <div style={{ display: 'flex', gap: 14, marginBottom: 16 }}>
        {plans.map(p => (
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
              <div style={{ borderTop: '1px solid #f0f0f0', marginTop: 12, paddingTop: 10, fontSize: 12, color: '#6a6e73', lineHeight: 1.45 }}>{p.models}{p.queue ? <><br />{p.queue}</> : null}</div>
            </div>
          </Card>
        ))}
      </div>
      <div style={{ display: 'grid', gridTemplateColumns: '1.2fr 1fr', gap: 14 }}>
        <div style={{ background: '#151515', borderRadius: 8, padding: '18px 20px', overflow: 'auto' }}>
          <div style={{ display: 'flex', alignItems: 'center', marginBottom: 10 }}>
            <div style={{ color: '#fff', fontFamily: DISPLAY, fontSize: 14, fontWeight: 600 }}>Compiled policy — {sel.name}</div>
            {sel.compiledPolicies && sel.compiledPolicies.length > 0 && (
              <Badge c={allUpToDate ? 'green' : 'gold'} style={{ marginLeft: 10 }}>{allUpToDate ? 'Up to date' : 'Drifted'}</Badge>
            )}
            <span style={{ marginLeft: 'auto', fontSize: 11, color: '#8a8d90', fontFamily: MONO }}>maas-default-gateway</span>
          </div>
          <pre style={{ margin: 0, fontFamily: MONO, fontSize: 11.5, lineHeight: 1.55, color: '#d2d2d2', whiteSpace: 'pre' }}>{planYaml(sel)}</pre>
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
          <Card>
            <div style={{ fontFamily: DISPLAY, fontSize: 14, fontWeight: 600, marginBottom: 8 }}>How enforcement layers stack</div>
            {ENF_LAYERS.map(e => (
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

/* ── Usage explorer ── */

const selStyle = { fontFamily: "'Red Hat Text',sans-serif", fontSize: 13, padding: '7px 10px', border: '1px solid #c7c7c7', borderRadius: 6, background: '#fff' };

function fmtNum(n) {
  if (n == null || isNaN(n)) return '0';
  if (n >= 1e9) return (n / 1e9).toFixed(2) + 'B';
  if (n >= 1e6) return (n / 1e6).toFixed(2) + 'M';
  if (n >= 1e3) return (n / 1e3).toFixed(1) + 'k';
  return String(Math.round(n));
}

function buildUsageDays(points) {
  const byDay = {};
  for (const p of points) {
    const day = p.start.slice(0, 10);
    if (!byDay[day]) byDay[day] = { input: 0, output: 0, reasoning: 0, cached: 0 };
    const q = p.quantities || {};
    byDay[day].input += q.prompt_tokens || 0;
    byDay[day].output += q.completion_tokens || 0;
    byDay[day].reasoning += q.reasoning_tokens || 0;
    byDay[day].cached += q.cached_tokens || 0;
  }
  const days = Object.entries(byDay).sort(([a], [b]) => a.localeCompare(b));
  const maxTotal = Math.max(1, ...days.map(([, d]) => d.input + d.output + d.reasoning + d.cached));
  const maxH = 118;
  return days.map(([day, d]) => {
    const total = d.input + d.output + d.reasoning + d.cached;
    const scale = total > 0 ? maxH / maxTotal : 0;
    const dt = new Date(day + 'T00:00:00Z');
    return {
      h1: Math.max(1, Math.round(d.input * scale)) + 'px',
      h2: Math.max(1, Math.round(d.output * scale)) + 'px',
      h3: Math.round(d.reasoning * scale) + 'px',
      h4: Math.round(d.cached * scale) + 'px',
      label: dt.toLocaleDateString('en-US', { month: 'short', day: 'numeric', timeZone: 'UTC' }),
      tip: `${dt.toLocaleDateString('en-US', { month: 'short', day: 'numeric', timeZone: 'UTC' })}: ${fmtNum(total)} tokens`,
    };
  });
}

function buildUsageRows(points) {
  const bySku = {};
  for (const p of points) {
    const key = p.sku + '|' + (p.geo || '');
    if (!bySku[key]) bySku[key] = { model: p.sku, geo: p.geo || '---', req: 0, tin: 0, tout: 0, trsn: 0, tcache: 0, cost: 0 };
    const q = p.quantities || {};
    bySku[key].req += p.events || 0;
    bySku[key].tin += q.prompt_tokens || 0;
    bySku[key].tout += q.completion_tokens || 0;
    bySku[key].trsn += q.reasoning_tokens || 0;
    bySku[key].tcache += q.cached_tokens || 0;
    bySku[key].cost += parseFloat(p.charged || '0');
  }
  const rawRows = Object.values(bySku);
  const rows = rawRows.map(r => ({
    model: r.model, geo: r.geo,
    req: fmtNum(r.req), tin: fmtNum(r.tin), tout: fmtNum(r.tout), trsn: fmtNum(r.trsn), tcache: fmtNum(r.tcache),
    cost: fmtMoney(r.cost.toFixed(2)), total: false,
  }));
  if (rows.length > 0) {
    const totReq = rawRows.reduce((s, r) => s + r.req, 0);
    const totCost = rawRows.reduce((s, r) => s + r.cost, 0);
    rows.push({ model: 'Total', geo: '', req: fmtNum(totReq), tin: '---', tout: '---', trsn: '---', tcache: '---', cost: fmtMoney(totCost.toFixed(2)), total: true });
  }
  return rows;
}

export function UsageExplorer({ apiUsage }) {
  const hasApi = apiUsage && apiUsage.points && apiUsage.points.length > 0;
  if (!hasApi) {
    return (
      <div data-screen-label="Usage explorer">
        <PageH title="Usage explorer" sub="Query the usage ledger. Every row is backed by per-request events, deduplicated on request_id." />
        <Card style={{ padding: '18px 20px' }}><Dim>No usage data available yet. Usage appears here once metering events flow through the pipeline.</Dim></Card>
      </div>
    );
  }
  const days = buildUsageDays(apiUsage.points);
  const usageRows = buildUsageRows(apiUsage.points);
  const cols = '1.8fr 1fr .9fr 1fr 1fr 1fr 1fr 1fr';
  return (
    <div data-screen-label="Usage explorer">
      <PageH title="Usage explorer" sub="Query the usage ledger. Every row is backed by per-request events, deduplicated on request_id." />
      <Card style={{ padding: '12px 16px', display: 'flex', gap: 10, alignItems: 'center', marginBottom: 14, flexWrap: 'wrap' }}>
        <select style={selStyle}><option>Last 14 days</option><option>MTD</option><option>Custom…</option></select>
        <select style={selStyle}><option>All tenants</option></select>
        <select style={selStyle}><option>All models</option></select>
        <select style={selStyle}><option>All geos</option></select>
        <select style={selStyle}><option>Group by: model</option><option>Group by: tenant</option><option>Group by: API key</option><option>Group by: geo</option></select>
        <div style={{ marginLeft: 'auto' }}><Btn small>Export</Btn></div>
      </Card>
      <div style={{ marginBottom: 14 }}><Stacked days={days} legend={USAGE_LEGEND} title="Tokens by type, daily" /></div>
      <Card style={{ padding: 0, overflow: 'hidden' }}>
        <THead cols={cols} labels={['Model', 'Geo', 'Requests', 'Input', 'Output', 'Reasoning', 'Cached', 'Billed']} />
        {usageRows.map((u, i) => (
          <TRow key={i} cols={cols} style={{ fontWeight: u.total ? 700 : 400, background: u.total ? '#fafafa' : '#fff' }}>
            <div>{u.model}</div><Dim>{u.geo}</Dim>
            <Mono>{u.req}</Mono><Mono>{u.tin}</Mono><Mono>{u.tout}</Mono><Mono>{u.trsn}</Mono><Mono>{u.tcache}</Mono><Mono>{u.cost}</Mono>
          </TRow>
        ))}
      </Card>
    </div>
  );
}

/* ── Billing operations ── */

function mapApiConnectors(connectors) {
  return connectors.map(c => ({
    name: c.name,
    status: c.connected ? 'Connected' : 'Disconnected',
    st: c.connected ? 'green' : 'red',
    top: c.type === 'stripe' ? '#635BFF' : '#8a8d90',
    desc: `Type: ${c.type}` + (c.sync ? ` · Usage sync: ${c.sync.usageRecordsInterval} · Invoice sync: ${c.sync.invoiceInterval}` : ''),
    action: 'Configure',
  }));
}

function mapApiInvoiceRuns(runs) {
  return runs.map(r => {
    const statuses = Object.keys(r.statusBreakdown || {});
    const mainStatus = statuses.includes('finalized') ? 'Settled' : statuses.includes('draft') ? 'Accruing' : statuses[0] || 'Unknown';
    const st = mainStatus === 'Settled' ? 'green' : mainStatus === 'Accruing' ? 'blue' : 'grey';
    return {
      period: r.period,
      count: String(r.tenantCount),
      amount: fmtMoney(r.totalAmount),
      status: mainStatus,
      st,
      action: mainStatus === 'Accruing' ? 'Preview' : 'View',
    };
  });
}

function mapApiWebhooks(webhooks) {
  return webhooks.map(w => {
    const code = w.status_code != null ? String(w.status_code) : 'pending';
    const ok = w.status_code != null && w.status_code >= 200 && w.status_code < 300;
    const dt = w.created_at ? new Date(w.created_at) : null;
    const when = dt ? dt.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false }) : '';
    return {
      event: w.event_type,
      dest: (() => { try { const h = new URL(w.endpoint).hostname; return w.tenant_id ? `${w.tenant_id} · ${h}` : h; } catch { return w.endpoint; } })(),
      code: ok ? code : w.attempts > 1 ? `${code} · retry` : code,
      st: ok ? 'green' : 'red',
      when,
    };
  });
}

function mapApiCredits(credits) {
  return credits.map(c => {
    const dt = new Date(c.created_at);
    return {
      text: `${c.kind === 'manual_credit' ? 'Credit' : c.kind} — ${c.reason}`,
      amt: fmtMoney(c.amount),
      when: dt.toLocaleDateString('en-US', { month: 'short', day: 'numeric' }),
    };
  });
}

function buildDunningFromTenants(tenants) {
  if (!tenants) return [];
  return tenants
    .filter(t => t.status === 'Dunning' || t.status === 'Frozen')
    .map(t => ({
      tenant: t.name || t.tenant_id,
      amount: fmtMoney(t.balance),
      detail: `Status: ${t.status} · Balance: ${fmtMoney(t.balance)}`,
      a1: 'View', a2: 'Freeze keys',
    }));
}

export function Billing({ apiConnectors, apiInvoiceRuns, apiWebhooks, apiCredits, apiTenants }) {
  const connectors = apiConnectors ? mapApiConnectors(apiConnectors) : [];
  const invoiceRuns = apiInvoiceRuns ? mapApiInvoiceRuns(apiInvoiceRuns) : [];
  const webhookLog = apiWebhooks ? mapApiWebhooks(apiWebhooks) : [];
  const grants = apiCredits ? mapApiCredits(apiCredits) : [];
  const dunningQueue = buildDunningFromTenants(apiTenants);

  return (
    <div data-screen-label="Billing operations">
      <PageH title="Billing operations" sub="Rated usage flows to the connected billing provider. The billing ledger stays the metering source of truth." />
      <div style={{ display: 'flex', gap: 14, marginBottom: 16 }}>
        {connectors.length > 0 ? connectors.map(c => (
          <Card key={c.name} style={{ flex: 1, borderTop: `3px solid ${c.top}` }}>
            <div style={{ display: 'flex', alignItems: 'center' }}>
              <div style={{ fontFamily: DISPLAY, fontSize: 15, fontWeight: 700 }}>{c.name}</div>
              <div style={{ marginLeft: 'auto' }}><Badge c={c.st}>{c.status}</Badge></div>
            </div>
            <div style={{ fontSize: 12.5, color: '#4d4d4d', marginTop: 8, lineHeight: 1.5 }}>{c.desc}</div>
            <div style={{ fontSize: 12, marginTop: 10 }}><a href="#" onClick={e => e.preventDefault()}>{c.action}</a></div>
          </Card>
        )) : (
          <Card style={{ flex: 1, padding: '18px 20px' }}>
            <Dim>No billing connectors configured. Create a BillingConnector CR to connect a payment provider.</Dim>
          </Card>
        )}
      </div>
      <div style={{ display: 'grid', gridTemplateColumns: '1.2fr 1fr', gap: 14, marginBottom: 14 }}>
        <Card style={{ padding: 0, overflow: 'hidden' }}>
          <div style={{ padding: '14px 18px', fontFamily: DISPLAY, fontSize: 15, fontWeight: 600 }}>Invoice runs</div>
          <THead cols="1fr .9fr 1fr 1fr 1fr" labels={['Period', 'Invoices', 'Amount', 'Status', '']} />
          {invoiceRuns.map(i => (
            <TRow key={i.period} cols="1fr .9fr 1fr 1fr 1fr">
              <div style={{ fontWeight: 600 }}>{i.period}</div><Mono>{i.count}</Mono><Mono>{i.amount}</Mono>
              <div><Badge c={i.st}>{i.status}</Badge></div>
              <div style={{ fontSize: 12 }}><a href="#" onClick={e => e.preventDefault()}>{i.action}</a></div>
            </TRow>
          ))}
          {invoiceRuns.length === 0 && <div style={{ padding: '12px 18px', fontSize: 12, color: '#6a6e73' }}>No invoice runs yet.</div>}
          <div style={{ padding: '12px 18px', borderTop: '1px solid #f0f0f0', fontSize: 12, color: '#6a6e73' }}>
            Prepaid reloads are recognized as deferred revenue; usage draws recognize on consumption (rev-rec export).
          </div>
        </Card>
        <Card style={{ padding: 0, overflow: 'hidden' }}>
          <div style={{ padding: '14px 18px', fontFamily: DISPLAY, fontSize: 15, fontWeight: 600 }}>Dunning queue</div>
          {dunningQueue.map(d => (
            <div key={d.tenant} style={{ padding: '12px 18px', borderTop: '1px solid #f0f0f0' }}>
              <div style={{ display: 'flex', fontSize: 13, alignItems: 'center' }}>
                <div style={{ fontWeight: 600 }}>{d.tenant}</div>
                <Mono style={{ marginLeft: 'auto' }}>{d.amount}</Mono>
              </div>
              <Dim style={{ marginTop: 4 }}>{d.detail}</Dim>
              <div style={{ display: 'flex', gap: 12, fontSize: 12, marginTop: 6 }}>
                <a href="#" onClick={e => e.preventDefault()}>{d.a1}</a>
                {d.a2 && <a href="#" onClick={e => e.preventDefault()} style={{ color: '#B1380B' }}>{d.a2}</a>}
              </div>
            </div>
          ))}
          {dunningQueue.length === 0 && <div style={{ padding: '12px 18px', fontSize: 12, color: '#6a6e73' }}>No tenants in dunning.</div>}
        </Card>
      </div>
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 14 }}>
        <Card style={{ padding: 0, overflow: 'hidden' }}>
          <div style={{ padding: '14px 18px', fontFamily: DISPLAY, fontSize: 15, fontWeight: 600 }}>Credit grants</div>
          {grants.map(g => (
            <div key={g.text} style={{ display: 'flex', gap: 10, padding: '10px 18px', borderTop: '1px solid #f0f0f0', fontSize: 12.5, alignItems: 'center' }}>
              <div style={{ flex: 1 }}>{g.text}</div><Mono>{g.amt}</Mono><div style={{ color: '#6a6e73', fontSize: 11 }}>{g.when}</div>
            </div>
          ))}
          {grants.length === 0 && <div style={{ padding: '12px 18px', fontSize: 12, color: '#6a6e73' }}>No credit grants recorded.</div>}
        </Card>
        <Card style={{ padding: 0, overflow: 'hidden' }}>
          <div style={{ padding: '14px 18px', fontFamily: DISPLAY, fontSize: 15, fontWeight: 600 }}>Webhook deliveries</div>
          {webhookLog.map((w, idx) => (
            <div key={w.event + w.when + idx} style={{ display: 'flex', gap: 10, padding: '9px 18px', borderTop: '1px solid #f0f0f0', fontSize: 12, alignItems: 'center' }}>
              <div style={{ fontFamily: MONO, color: '#4d4d4d', flex: 1 }}>{w.event}</div>
              <div style={{ color: '#6a6e73' }}>{w.dest}</div>
              <Badge c={w.st} style={{ padding: '1px 8px', fontSize: 11 }}>{w.code}</Badge>
              <div style={{ color: '#8a8d90', fontSize: 11, width: 52, textAlign: 'right' }}>{w.when}</div>
            </div>
          ))}
          {webhookLog.length === 0 && <div style={{ padding: '12px 18px', fontSize: 12, color: '#6a6e73' }}>No webhook deliveries recorded.</div>}
        </Card>
      </div>
    </div>
  );
}

/* ── Cost & margin ── */

export function Cost({ apiOverview }) {
  const topModels = apiOverview && apiOverview.topModels
    ? apiOverview.topModels.map(m => ({ name: m.sku, rev: fmtMoney(m.amount) }))
    : [];
  return (
    <div data-screen-label="Cost & margin">
      <PageH title="Cost & margin" sub="Per-SKU revenue analysis. Full cost modeling, GPU fleet economics, and pass-through margin tracking arrive in M8." />
      {topModels.length > 0 ? (
        <Card style={{ padding: 0, overflow: 'hidden' }}>
          <div style={{ padding: '14px 18px', fontFamily: DISPLAY, fontSize: 15, fontWeight: 600 }}>Revenue by SKU (MTD)</div>
          <THead cols="2fr 1fr" labels={['Model', 'Revenue']} />
          {topModels.map(m => (
            <TRow key={m.name} cols="2fr 1fr">
              <div style={{ fontWeight: 600 }}>{m.name}</div>
              <Mono>{m.rev}</Mono>
            </TRow>
          ))}
        </Card>
      ) : (
        <Card style={{ padding: '18px 20px' }}><Dim>No revenue data available yet.</Dim></Card>
      )}
      <div style={{ background: '#E0F0FF', borderRadius: 8, padding: '14px 16px', fontSize: 12.5, color: '#003366', lineHeight: 1.55, marginTop: 14 }}>
        <b>Coming in M8:</b> GPU fleet cost tracking, per-model unit economics, pass-through provider margin analysis, and automated price book recommendations based on cost trends.
      </div>
    </div>
  );
}

/* ── Metering pipeline health ── */

export function Metering({ apiHealth, apiDrift }) {
  const dlqDepth = apiHealth?.dlqDepth;
  const watermark = apiHealth?.watermark;
  const driftExceeded = apiHealth?.driftExceeded;
  const ingestRate = apiHealth?.ingestRate;

  const lagSec = watermark && watermark !== '0001-01-01T00:00:00Z'
    ? Math.round((Date.now() - new Date(watermark).getTime()) / 1000) : null;
  const healthy = apiHealth != null && (dlqDepth == null || dlqDepth < 100) && (lagSec == null || lagSec < 60);

  const meterKpis = apiHealth ? [
    { label: 'DLQ depth', value: dlqDepth != null ? String(dlqDepth) : '---', delta: dlqDepth > 0 ? 'events awaiting replay' : 'all clear', border: dlqDepth > 100 ? '#B1380B' : '#3D7317' },
    { label: 'Ingest watermark lag', value: lagSec != null ? `${lagSec}s` : '---', delta: 'time behind real-time', border: lagSec != null && lagSec > 60 ? '#B1380B' : '#3D7317' },
    { label: 'Ingest rate (1h)', value: ingestRate != null ? fmtTokenCount(ingestRate) + ' events' : '---', delta: 'events ingested in the last hour', border: '#3D7317' },
  ] : [];

  const driftReports = apiDrift && apiDrift.reports ? apiDrift.reports : [];

  return (
    <div data-screen-label="Metering pipeline health">
      <PageH title="Metering pipeline health" sub="Per-request usage events from the gateway into the billing ledger. This page is why tenants trust the invoice." />
      {apiHealth ? (
        <>
          <Card style={{ padding: '14px 18px', marginBottom: 14, borderLeft: `4px solid ${healthy ? '#3D7317' : '#B1380B'}` }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
              <div style={{ width: 9, height: 9, borderRadius: '50%', background: healthy ? '#3D7317' : '#B1380B' }} />
              <div style={{ fontFamily: DISPLAY, fontSize: 14, fontWeight: 600 }}>Pipeline {healthy ? 'healthy' : 'degraded'}</div>
            </div>
          </Card>
          <div style={{ display: 'flex', gap: 14, marginBottom: 16 }}>{meterKpis.map(k => <Kpi key={k.label} {...k} />)}</div>
        </>
      ) : (
        <Card style={{ padding: '18px 20px', marginBottom: 14 }}><Dim>Connecting to metering health endpoint...</Dim></Card>
      )}
      {driftReports.length > 0 && (
        <Card style={{ padding: 0, overflow: 'hidden', marginBottom: 14 }}>
          <div style={{ padding: '14px 18px', fontFamily: DISPLAY, fontSize: 15, fontWeight: 600 }}>Drift reports</div>
          <THead cols="1fr 1.6fr 1fr 1fr .8fr .9fr" labels={['Window', 'Tenant', 'Prom tok', 'Ledger tok', 'Drift', 'Status']} />
          {driftReports.map((r, i) => {
            const dp = r.drift_pct != null ? parseFloat(r.drift_pct) : null;
            const driftPct = dp != null ? `${dp > 0 ? '+' : ''}${dp.toFixed(2)}%` : '---';
            const ok = !r.exceeded;
            const windowLabel = r.window_start ? new Date(r.window_start).toLocaleDateString('en-US', { month: 'short', day: 'numeric', timeZone: 'UTC' }) : '---';
            return (
              <TRow key={i} cols="1fr 1.6fr 1fr 1fr .8fr .9fr" style={{ fontSize: 12.5 }}>
                <Dim>{windowLabel}</Dim>
                <div style={{ fontWeight: 600 }}>{r.tenant_id || '---'}</div>
                <Mono>{fmtTokenCount(r.prom_tokens)}</Mono>
                <Mono>{fmtTokenCount(r.ledger_tokens)}</Mono>
                <Mono style={{ color: ok ? '#204D00' : '#73480B' }}>{driftPct}</Mono>
                <div><Badge c={ok ? 'green' : 'gold'}>{ok ? 'Within SLO' : 'Investigate'}</Badge></div>
              </TRow>
            );
          })}
        </Card>
      )}
      <div style={{ background: '#E0F0FF', borderRadius: 8, padding: '14px 16px', fontSize: 12.5, color: '#003366', lineHeight: 1.55 }}>
        <b>Architecture note:</b> Limitador enforces, it does not meter. Billing events come from structured gateway access logs shipped via the capture filter to the billing ledger. The ledger is the source of truth; balance enforcement tolerates drift between Limitador counters and the ledger.
      </div>
    </div>
  );
}

/* ── API surface ── */

export function ApiSurface() {
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
            {API_HEADERS.map(h => (
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
            {API_MGMT.map(m => (
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
              {WEBHOOK_TYPES.map(w => (
                <span key={w} style={{ fontFamily: MONO, fontSize: 11.5, background: '#f5f5f5', borderRadius: 12, padding: '4px 12px', color: '#4d4d4d' }}>{w}</span>
              ))}
            </div>
            <Pre style={{ marginTop: 12 }}>{jsonWebhook}</Pre>
          </Card>
          <Card style={{ padding: '18px 20px' }}>
            <div style={{ fontFamily: DISPLAY, fontSize: 15, fontWeight: 700 }}>5 · Control plane (what it compiles to)</div>
            {CONTROL_PLANE.map(c => (
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
