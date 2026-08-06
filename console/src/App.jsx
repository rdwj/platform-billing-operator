import React, { useEffect, useMemo, useState } from 'react';
import { fmtMoney, badgeColor, fmtTokenCount } from './utils.js';
import { DISPLAY, PAL } from './ui.jsx';
import { Overview, Models, Tenants, TenantDetail, Plans, UsageExplorer, Billing, Cost, Metering, ApiSurface } from './screens-provider.jsx';
import { TOverview, TModels, TUsage, TBilling, TBudgets, TKeys, TReservations } from './screens-tenant.jsx';
import CapacityCalendar from './screens-reservations.jsx';
import Integrations from './screens-integrations.jsx';
import { fetchTenants, fetchLedger, fetchBudgets, fetchKeys, fetchExport, fetchPlans, fetchPriceBooks, fetchUsage, fetchOverview, fetchConnectors, fetchWebhooks, fetchInvoiceRuns, fetchAdjustmentsAll, fetchHealth, fetchDriftAll, fetchPricing, fetchInvoices, fetchReservations, createReservation, cancelReservation } from './api.js';

function buildTenantDetail(t, credited, frozen) {
  const bal = t.balance ? parseFloat(t.balance) : null;
  const crd = credited[t.tenant_id] ? 1000 : 0;
  const adjBal = bal !== null ? bal + crd : null;
  const burn = t.burn_per_day ? parseFloat(t.burn_per_day) : null;
  const runway = t.runway_days ? parseFloat(t.runway_days) : null;
  const isFrozen = frozen[t.tenant_id];
  const st = isFrozen ? 'Frozen' : t.status;
  return {
    tid: t.tenant_id, name: t.name, plan: t.plan, mode: t.billing_mode || '---',
    status: st, st: badgeColor(isFrozen ? 'Frozen' : t.status),
    balance: adjBal !== null ? fmtMoney(String(adjBal)) : '---',
    balColor: adjBal !== null && adjBal < 1300 ? '#B1380B' : '#151515',
    runway: adjBal === null ? 'Invoiced tenant — no prepaid balance'
      : adjBal === 0 ? 'Depleted' : runway ? `≈ ${runway.toFixed(1)} days of runway at ${fmtMoney(t.burn_per_day)}/day burn` : '---',
    gaugeW: adjBal === null ? '100%' : Math.min(100, Math.round((adjBal / 25000) * 100)) + '%',
    gaugeColor: adjBal !== null && adjBal < 1300 ? '#B1380B' : adjBal !== null && adjBal < 5000 ? '#F0AB00' : '#3D7317',
    arState: t.reload_enabled ? 'Enabled' : adjBal === null ? 'N/A (invoiced)' : 'Off',
    arWarn: false,
    arThreshold: t.reload_threshold ? fmtMoney(t.reload_threshold) : '---',
    arAmount: t.reload_amount ? fmtMoney(t.reload_amount) : '---',
    arMethod: adjBal === null ? 'Invoice' : 'Stripe',
    arCap: t.monthly_cap ? fmtMoney(t.monthly_cap) + ' / mo' : '---',
    arLast: '---',
    overrun: burn ? fmtMoney(String(Math.round(burn / 288))) : '$0',
    mtd: fmtMoney(t.mtd_spend), burn: burn ? fmtMoney(t.burn_per_day) + '/d' : '---',
    reload: t.reload_enabled ? 'Enabled' : 'Off',
  };
}

function mapLedgerEntry(e) {
  const typeMap = { charge: 'Charge', manual_credit: 'Credit', manual_debit: 'Debit', reload: 'Reload', payment_credit: 'Credit', balance_carry_in: 'Carry-in', writeoff: 'Write-off', sla_credit: 'SLA credit', promo: 'Promo' };
  const colorMap = { charge: 'grey', manual_credit: 'blue', manual_debit: 'red', reload: 'green', payment_credit: 'green', balance_carry_in: 'blue', writeoff: 'red', sla_credit: 'blue', promo: 'purple' };
  const amt = parseFloat(e.amount);
  return {
    date: new Date(e.timestamp).toLocaleDateString('en-US', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }),
    type: typeMap[e.type] || e.type,
    detail: e.description,
    amt: (amt >= 0 ? '+' : '') + fmtMoney(e.amount),
    after: fmtMoney(e.balance_after),
    c: colorMap[e.type] || 'grey',
    amtColor: amt >= 0 ? '#204D00' : '#731F00',
  };
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
      tip: `${dt.toLocaleDateString('en-US', { month: 'short', day: 'numeric', timeZone: 'UTC' })}: ${fmtTokenCount(total)} tokens`,
    };
  });
}

const PROVIDER_NAV = [
  { hdr: 'Monetize' },
  { id: 'overview', label: 'Overview' }, { id: 'models', label: 'Models & pricing' }, { id: 'plans', label: 'Plans & rate limits' },
  { hdr: 'Customers' },
  { id: 'tenants', label: 'Tenants' }, { id: 'usage', label: 'Usage explorer' },
  { hdr: 'Operate' },
  { id: 'reservations', label: 'GPU reservations' }, { id: 'integrations', label: 'Integrations' }, { id: 'billing', label: 'Billing operations' }, { id: 'cost', label: 'Cost & margin' }, { id: 'metering', label: 'Metering health' },
  { hdr: 'Reference' },
  { id: 'api', label: 'API surface' }
];

const TENANT_NAV = [
  { hdr: 'Tenant portal' },
  { id: 't-overview', label: 'Overview' }, { id: 't-models', label: 'Model directory' }, { id: 't-usage', label: 'Usage & reports' },
  { hdr: 'Billing' },
  { id: 't-billing', label: 'Balance & billing' }, { id: 't-budgets', label: 'Budgets & alerts' }, { id: 't-keys', label: 'API keys' },
  { hdr: 'GPU' },
  { id: 't-reservations', label: 'Reservations' }
];

export default function App() {
  const [persona, setPersona] = useState('provider');
  const [screen, setScreen] = useState('overview');
  const [tScreen, setTScreen] = useState('t-overview');
  const [tenant, setTenant] = useState(null);
  const [tab, setTab] = useState('balance');
  const [plan, setPlan] = useState('Priority');
  const [arOn, setArOn] = useState(true);
  const [extraKeys, setExtraKeys] = useState([]);
  const [secret, setSecret] = useState(null);
  const [credited, setCredited] = useState({});
  const [frozen, setFrozen] = useState({});

  // --- API data layer ---
  const [apiTenants, setApiTenants] = useState(null);
  const [apiLedger, setApiLedger] = useState(null);
  const [apiBudgets, setApiBudgets] = useState(null);
  const [apiKeys, setApiKeys] = useState(null);
  const [apiExport, setApiExport] = useState(null);
  const [apiPlans, setApiPlans] = useState(null);
  const [apiPriceBooks, setApiPriceBooks] = useState(null);
  const [apiUsage, setApiUsage] = useState(null);
  const [apiOverview, setApiOverview] = useState(null);
  const [apiConnectors, setApiConnectors] = useState(null);
  const [apiWebhooks, setApiWebhooks] = useState(null);
  const [apiInvoiceRuns, setApiInvoiceRuns] = useState(null);
  const [apiCredits, setApiCredits] = useState(null);
  const [apiHealth, setApiHealth] = useState(null);
  const [apiDrift, setApiDrift] = useState(null);
  const [apiPortalPricing, setApiPortalPricing] = useState(null);
  const [apiPortalInvoices, setApiPortalInvoices] = useState(null);
  const [apiPortalUsage, setApiPortalUsage] = useState(null);
  const [apiReservations, setApiReservations] = useState(null);
  const [apiPortalReservations, setApiPortalReservations] = useState(null);

  useEffect(() => {
    fetchTenants().then(data => {
      if (data && data.tenants) setApiTenants(data.tenants);
    });
    fetchPlans().then(data => {
      if (data && data.plans) setApiPlans(data.plans);
    });
    fetchPriceBooks().then(data => {
      if (data && data.pricebooks) setApiPriceBooks(data.pricebooks);
    });
    const now = new Date();
    const from14 = new Date(now.getTime() - 14 * 86400000).toISOString();
    fetchUsage({ from: from14, to: now.toISOString(), bucket: 'day' }).then(data => {
      if (data) setApiUsage(data);
    });
    fetchOverview().then(data => { if (data) setApiOverview(data); });
    fetchConnectors().then(data => { if (data && data.connectors) setApiConnectors(data.connectors); });
    fetchWebhooks(20).then(data => { if (data && data.webhooks) setApiWebhooks(data.webhooks); });
    fetchInvoiceRuns().then(data => { if (data && data.invoiceRuns) setApiInvoiceRuns(data.invoiceRuns); });
    fetchAdjustmentsAll('manual_credit', 20).then(data => { if (data && data.adjustments) setApiCredits(data.adjustments); });
    fetchHealth().then(data => { if (data) setApiHealth(data); });
    fetchDriftAll().then(data => { if (data) setApiDrift(data); });
    fetchReservations().then(data => { if (data) setApiReservations(data); });
  }, []);

  // Load tenant-specific data when a tenant is selected (provider drill-in)
  useEffect(() => {
    if (!tenant) {
      setApiLedger(null);
      setApiBudgets(null);
      setApiKeys(null);
      setApiExport(null);
      return;
    }
    const tid = tenant.tenant_id || tenant.tid;
    fetchLedger(tid).then(data => { if (data) setApiLedger(data); });
    fetchBudgets(tid).then(data => { if (data) setApiBudgets(data); });
    fetchKeys(tid).then(data => { if (data) setApiKeys(data); });
    const now = new Date();
    const from = new Date(now.getFullYear(), now.getMonth(), 1).toISOString();
    fetchExport({ tenant: tid, from, to: now.toISOString() }).then(data => { if (data) setApiExport(data); });
  }, [tenant?.tenant_id, tenant?.tid]);

  // Auto-select portal tenant and fetch tenant-specific data for tenant persona
  useEffect(() => {
    if (persona !== 'tenant' || !apiTenants || apiTenants.length === 0) return;
    const first = apiTenants[0];
    setTenant(first);
    const tid = first.tenant_id;
    fetchPricing(tid).then(data => { if (data) setApiPortalPricing(data); });
    fetchInvoices(tid).then(data => { if (data) setApiPortalInvoices(data); });
    const now = new Date();
    const from14 = new Date(now.getTime() - 14 * 86400000).toISOString();
    fetchUsage({ tenant: tid, from: from14, to: now.toISOString(), bucket: 'day' }).then(data => { if (data) setApiPortalUsage(data); });
    fetchReservations(tid).then(data => { if (data) setApiPortalReservations(data); });
  }, [persona, apiTenants]);

  const prov = persona === 'provider';
  const nav = prov ? PROVIDER_NAV : TENANT_NAV;
  const active = prov ? screen : tScreen;

  // Provider tenant detail
  const selectedTenantApi = tenant && apiTenants ? apiTenants.find(t => t.tenant_id === (tenant.tenant_id || tenant.tid)) : null;
  const td = selectedTenantApi ? buildTenantDetail(selectedTenantApi, credited, frozen) : null;

  // Tenant portal detail
  const portalTenant = apiTenants && apiTenants.length > 0 ? apiTenants[0] : null;
  const th = portalTenant ? buildTenantDetail(portalTenant, credited, frozen) : null;

  // Shared ledger view
  const ledgerView = apiLedger && apiLedger.entries ? apiLedger.entries.map(mapLedgerEntry) : [];

  // Usage days: tenant-specific for portal, global for provider UsageExplorer
  const portalUsageSource = apiPortalUsage || apiUsage;
  const usageDays = portalUsageSource && portalUsageSource.points ? buildUsageDays(portalUsageSource.points) : [];

  // Budget summary for tenant portal
  const portalBudgets = apiBudgets && apiBudgets.budgets ? apiBudgets.budgets : [];

  // Portal invoices
  const portalInvoices = apiPortalInvoices && apiPortalInvoices.invoices ? apiPortalInvoices.invoices : [];

  // Portal pricing
  const portalPricing = apiPortalPricing && apiPortalPricing.skus ? apiPortalPricing.skus : [];

  // Portal keys for tenant view
  const portalKeys = apiKeys && apiKeys.keys
    ? apiKeys.keys.map(k => ({
        name: k.key_name, prefix: k.key_name, scopes: k.source || '---',
        limit: '---', last: k.created_at ? new Date(k.created_at).toLocaleDateString('en-US', { month: 'short', day: 'numeric' }) : '---',
        status: k.enabled ? 'Active' : 'Disabled', st: k.enabled ? 'green' : 'grey',
      }))
    : [];

  // Reservations (provider = all, tenant portal = per-tenant)
  const allReservations = apiReservations && apiReservations.reservations ? apiReservations.reservations : [];
  const portalReservations = apiPortalReservations && apiPortalReservations.reservations ? apiPortalReservations.reservations : [];

  const handleCreateReservation = async body => {
    const result = await createReservation(body);
    if (!result.error) {
      fetchReservations().then(data => { if (data) setApiReservations(data); });
      if (portalTenant) {
        fetchReservations(portalTenant.tenant_id).then(data => { if (data) setApiPortalReservations(data); });
      }
    }
  };

  const handleCancelReservation = async name => {
    const result = await cancelReservation(name);
    if (!result.error) {
      fetchReservations().then(data => { if (data) setApiReservations(data); });
      if (portalTenant) {
        fetchReservations(portalTenant.tenant_id).then(data => { if (data) setApiPortalReservations(data); });
      }
    }
  };

  const openTenant = t => { setTenant(t); setScreen('tenants'); setTab('balance'); };
  const goNav = id => { if (prov) { setScreen(id); setTenant(null); } else setTScreen(id); };
  const createKey = () => {
    const n = extraKeys.length + 1;
    const s = 'maas_sk_live_' + Math.random().toString(36).slice(2, 10) + Math.random().toString(36).slice(2, 10);
    setExtraKeys([...extraKeys, { name: 'new-key-' + n, prefix: s.slice(0, 12) + '…' + s.slice(-4), scopes: 'all models · any geo', limit: '100k TPM', last: 'never', status: 'Active', st: 'green' }]);
    setSecret(s);
  };

  const pill = on => ({ padding: '4px 14px', borderRadius: 13, fontSize: 12, cursor: 'pointer', fontWeight: 600, background: on ? '#0066CC' : 'transparent', color: on ? '#fff' : '#b8bbbe' });

  return (
    <div style={{ height: '100vh', display: 'flex', flexDirection: 'column', fontFamily: "'Red Hat Text',sans-serif", color: '#151515', background: '#f2f2f2', overflow: 'hidden' }}>
      <div style={{ height: 56, background: '#151515', display: 'flex', alignItems: 'center', gap: 14, padding: '0 20px', flexShrink: 0 }}>
        <div style={{ width: 26, height: 26, background: '#EE0000', borderRadius: 6, flexShrink: 0 }} />
        <div style={{ color: '#fff', fontFamily: DISPLAY, fontWeight: 700, fontSize: 15 }}>
          Redwoods University&nbsp;&nbsp;<span style={{ color: '#8a8d90', fontWeight: 400 }}>MaaS FinOps Console</span>
        </div>
        <div style={{ flex: 1 }} />
        <div style={{ display: 'flex', background: '#2b2e33', borderRadius: 16, padding: 3, gap: 2 }}>
          <div onClick={() => setPersona('provider')} style={pill(prov)}>Provider</div>
          <div onClick={() => setPersona('tenant')} style={pill(!prov)}>Tenant portal</div>
        </div>
        <div style={{ fontSize: 12, color: '#d2d2d2', border: '1px solid #4d4d4d', borderRadius: 12, padding: '3px 12px' }}>
          {!prov && th ? `${th.name} · tenant workspace` : 'production'}
        </div>
        <div style={{ width: 30, height: 30, borderRadius: '50%', background: '#0066CC', color: '#fff', display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: 12, fontWeight: 700 }}>MG</div>
      </div>
      <div style={{ flex: 1, display: 'flex', overflow: 'hidden' }}>
        <div style={{ width: 216, background: '#212427', overflowY: 'auto', padding: '10px 0', flexShrink: 0 }}>
          {nav.map((n, i) => n.hdr
            ? <div key={i} style={{ padding: '16px 16px 6px', fontSize: 10, fontWeight: 700, letterSpacing: '.08em', color: '#8a8d90', textTransform: 'uppercase' }}>{n.hdr}</div>
            : <div key={n.id} onClick={() => goNav(n.id)} style={{
                display: 'flex', alignItems: 'center', padding: '9px 16px 9px 13px', cursor: 'pointer', fontSize: 14,
                borderLeft: `3px solid ${active === n.id ? '#0066CC' : 'transparent'}`,
                background: active === n.id ? '#3c3f42' : 'transparent',
                color: active === n.id ? '#fff' : '#d2d2d2'
              }}>{n.label}</div>
          )}
        </div>
        <div style={{ flex: 1, overflowY: 'auto' }}>
          <div style={{ padding: '22px 28px 40px', maxWidth: 1320 }}>
            {prov && screen === 'overview' && <Overview apiTenants={apiTenants} apiOverview={apiOverview} onTenant={openTenant} goMetering={() => setScreen('metering')} />}
            {prov && screen === 'models' && <Models apiPriceBooks={apiPriceBooks} />}
            {prov && screen === 'plans' && <Plans plan={plan} setPlan={setPlan} apiPlans={apiPlans} />}
            {prov && screen === 'tenants' && !tenant && <Tenants apiTenants={apiTenants} onTenant={openTenant} />}
            {prov && screen === 'tenants' && tenant && td && (
              <TenantDetail td={td} tab={tab} setTab={setTab} ledger={ledgerView}
                apiBudgets={apiBudgets} apiKeys={apiKeys} apiExport={apiExport}
                back={() => setTenant(null)} goPlans={() => { setScreen('plans'); setTenant(null); }}
                grantCredit={() => !credited[td.tid] && setCredited({ ...credited, [td.tid]: true })}
                grantLabel={credited[td.tid] ? 'Credited +$1,000' : 'Grant credit'}
                toggleFreeze={() => setFrozen({ ...frozen, [td.tid]: !frozen[td.tid] })}
                freezeLabel={frozen[td.tid] ? 'Unfreeze keys' : 'Freeze keys'} />
            )}
            {prov && screen === 'reservations' && <CapacityCalendar reservations={allReservations} />}
            {prov && screen === 'usage' && <UsageExplorer apiUsage={apiUsage} />}
            {prov && screen === 'integrations' && <Integrations />}
            {prov && screen === 'billing' && <Billing apiConnectors={apiConnectors} apiInvoiceRuns={apiInvoiceRuns} apiWebhooks={apiWebhooks} apiCredits={apiCredits} apiTenants={apiTenants} />}
            {prov && screen === 'cost' && <Cost apiOverview={apiOverview} />}
            {prov && screen === 'metering' && <Metering apiHealth={apiHealth} apiDrift={apiDrift} />}
            {prov && screen === 'api' && <ApiSurface />}
            {!prov && th && tScreen === 't-overview' && <TOverview th={th} ledger={ledgerView} goTo={setTScreen} usageDays={usageDays} budgets={portalBudgets} />}
            {!prov && th && tScreen === 't-models' && <TModels pricing={portalPricing} planName={th.plan} />}
            {!prov && th && tScreen === 't-usage' && <TUsage usageDays={usageDays} keyUsage={[]} />}
            {!prov && th && tScreen === 't-billing' && <TBilling th={th} ledger={ledgerView} arOn={arOn} toggleAr={() => setArOn(!arOn)} invoices={portalInvoices} />}
            {!prov && th && tScreen === 't-budgets' && <TBudgets apiBudgets={apiBudgets} />}
            {!prov && th && tScreen === 't-keys' && <TKeys keys={portalKeys.concat(extraKeys)} createKey={createKey} secret={secret} dismiss={() => setSecret(null)} />}
            {!prov && th && tScreen === 't-reservations' && <TReservations reservations={portalReservations} onCreateReservation={handleCreateReservation} onCancelReservation={handleCancelReservation} />}
            {!prov && !th && <div style={{ padding: '40px 0', textAlign: 'center', color: '#6a6e73' }}>Loading tenant portal...</div>}
          </div>
        </div>
      </div>
    </div>
  );
}
