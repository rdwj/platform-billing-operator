import React, { useEffect, useMemo, useState } from 'react';
import { makeData, mkDetail } from './data.js';
import { DISPLAY, PAL } from './ui.jsx';
import { Overview, Models, Tenants, TenantDetail, Plans, UsageExplorer, Billing, Cost, Metering, ApiSurface } from './screens-provider.jsx';
import { TOverview, TModels, TUsage, TBilling, TBudgets, TKeys } from './screens-tenant.jsx';
import Integrations from './screens-integrations.jsx';
import { fetchTenants, fetchLedger, fetchBudgets, fetchKeys, fetchExport, fetchPlans, fetchPriceBooks, fetchUsage } from './api.js';

function fmtMoney(v) {
  if (!v || v === '0' || v === '0.000000') return '$0.00';
  const n = parseFloat(v);
  if (isNaN(n)) return '---';
  const abs = Math.abs(n);
  const s = abs >= 1000 ? '$' + abs.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
    : abs >= 1 ? '$' + abs.toFixed(2) : '$' + abs.toFixed(4);
  return n < 0 ? '−' + s : s;
}

function badgeColor(status) {
  const map = { Active: 'green', Warning: 'gold', Grace: 'gold', Dunning: 'red', Frozen: 'red', Suspended: 'red', Pending: 'grey', Closed: 'grey' };
  return map[status] || 'grey';
}

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

const PROVIDER_NAV = [
  { hdr: 'Monetize' },
  { id: 'overview', label: 'Overview' }, { id: 'models', label: 'Models & pricing' }, { id: 'plans', label: 'Plans & rate limits' },
  { hdr: 'Customers' },
  { id: 'tenants', label: 'Tenants' }, { id: 'usage', label: 'Usage explorer' },
  { hdr: 'Operate' },
  { id: 'integrations', label: 'Integrations' }, { id: 'billing', label: 'Billing operations' }, { id: 'cost', label: 'Cost & margin' }, { id: 'metering', label: 'Metering health' },
  { hdr: 'Reference' },
  { id: 'api', label: 'API surface' }
];

const TENANT_NAV = [
  { hdr: 'Helix Biotech' },
  { id: 't-overview', label: 'Overview' }, { id: 't-models', label: 'Model directory' }, { id: 't-usage', label: 'Usage & reports' },
  { hdr: 'Billing' },
  { id: 't-billing', label: 'Balance & billing' }, { id: 't-budgets', label: 'Budgets & alerts' }, { id: 't-keys', label: 'API keys' }
];

export default function App() {
  const [persona, setPersona] = useState('provider');
  const [screen, setScreen] = useState('overview');
  const [tScreen, setTScreen] = useState('t-overview');
  const [tenant, setTenant] = useState(null);
  const [tab, setTab] = useState('balance');
  const [plan, setPlan] = useState('Priority');
  const [inc, setInc] = useState(false);
  const [arOn, setArOn] = useState(true);
  const [extraKeys, setExtraKeys] = useState([]);
  const [secret, setSecret] = useState(null);
  const [dunningRetried, setDunningRetried] = useState(false);
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
  }, []);

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

  const D = useMemo(() => makeData(inc), [inc]);

  const prov = persona === 'provider';
  const nav = prov ? PROVIDER_NAV : TENANT_NAV;
  const active = prov ? screen : tScreen;

  const selectedTenantApi = tenant && apiTenants ? apiTenants.find(t => t.tenant_id === (tenant.tenant_id || tenant.tid)) : null;
  const mockTenant = tenant ? (D.tenants.find(x => x.tid === (tenant.tid || tenant.tenant_id)) || D.tenants[1]) : D.tenants[1];
  const td = selectedTenantApi ? buildTenantDetail(selectedTenantApi, credited, frozen) : mkDetail(mockTenant, credited, frozen);
  const th = mkDetail(D.tenants[1], credited, frozen);
  const ledgerView = apiLedger && apiLedger.entries ? apiLedger.entries.map(mapLedgerEntry) : D.ledger;

  const dunning = D.dunningBase.map((d, i) => ({
    ...d,
    ...(i === 0 && dunningRetried ? { detail: 'Retry in flight — attempt 3 of 4 · initiated just now from console', a1: 'Retrying…' } : {}),
    retry: i === 0 ? () => setDunningRetried(true) : undefined
  }));

  const openTenant = t => { setTenant(t); setScreen('tenants'); setTab('balance'); };
  const goNav = id => { if (prov) { setScreen(id); setTenant(null); } else setTScreen(id); };
  const createKey = () => {
    const n = extraKeys.length + 1;
    const s = 'maas_sk_live_' + Math.random().toString(36).slice(2, 10) + Math.random().toString(36).slice(2, 10);
    setExtraKeys([...extraKeys, { name: 'new-key-' + n, prefix: s.slice(0, 12) + '…' + s.slice(-4), scopes: 'all Priority models · any geo', limit: '100k TPM', last: 'never', status: 'Active', st: 'green' }]);
    setSecret(s);
  };

  const pill = on => ({ padding: '4px 14px', borderRadius: 13, fontSize: 12, cursor: 'pointer', fontWeight: 600, background: on ? '#0066CC' : 'transparent', color: on ? '#fff' : '#b8bbbe' });

  return (
    <div style={{ height: '100vh', display: 'flex', flexDirection: 'column', fontFamily: "'Red Hat Text',sans-serif", color: '#151515', background: '#f2f2f2', overflow: 'hidden' }}>
      <div style={{ height: 56, background: '#151515', display: 'flex', alignItems: 'center', gap: 14, padding: '0 20px', flexShrink: 0 }}>
        <div style={{ width: 26, height: 26, background: '#EE0000', borderRadius: 6, flexShrink: 0 }} />
        <div style={{ color: '#fff', fontFamily: DISPLAY, fontWeight: 700, fontSize: 15 }}>
          Corvid AI Cloud&nbsp;&nbsp;<span style={{ color: '#8a8d90', fontWeight: 400 }}>MaaS FinOps Console</span>
        </div>
        <div style={{ flex: 1 }} />
        <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 12, color: '#8a8d90', cursor: 'pointer' }}>
          <input type="checkbox" checked={inc} onChange={e => setInc(e.target.checked)} /> incident demo
        </label>
        <div style={{ display: 'flex', background: '#2b2e33', borderRadius: 16, padding: 3, gap: 2 }}>
          <div onClick={() => setPersona('provider')} style={pill(prov)}>Provider</div>
          <div onClick={() => setPersona('tenant')} style={pill(!prov)}>Tenant portal</div>
        </div>
        <div style={{ fontSize: 12, color: '#d2d2d2', border: '1px solid #4d4d4d', borderRadius: 12, padding: '3px 12px' }}>
          {!prov ? 'Helix Biotech · tenant workspace' : inc ? 'production · degraded' : 'production'}
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
            {prov && screen === 'overview' && <Overview D={D} apiTenants={apiTenants} onTenant={openTenant} goMetering={() => setScreen('metering')} />}
            {prov && screen === 'models' && <Models D={D} apiPriceBooks={apiPriceBooks} />}
            {prov && screen === 'plans' && <Plans D={D} plan={plan} setPlan={setPlan} apiPlans={apiPlans} />}
            {prov && screen === 'tenants' && !tenant && <Tenants D={D} apiTenants={apiTenants} onTenant={openTenant} />}
            {prov && screen === 'tenants' && tenant && (
              <TenantDetail D={D} td={td} tab={tab} setTab={setTab} ledger={ledgerView}
                apiBudgets={apiBudgets} apiKeys={apiKeys} apiExport={apiExport}
                back={() => setTenant(null)} goPlans={() => { setScreen('plans'); setTenant(null); }}
                grantCredit={() => !credited[td.tid] && setCredited({ ...credited, [td.tid]: true })}
                grantLabel={credited[td.tid] ? 'Credited +$1,000' : 'Grant credit'}
                toggleFreeze={() => setFrozen({ ...frozen, [td.tid]: !frozen[td.tid] })}
                freezeLabel={frozen[td.tid] ? 'Unfreeze keys' : 'Freeze keys'} />
            )}
            {prov && screen === 'usage' && <UsageExplorer D={D} apiUsage={apiUsage} />}
            {prov && screen === 'integrations' && <Integrations />}
            {prov && screen === 'billing' && <Billing D={D} dunning={dunning} />}
            {prov && screen === 'cost' && <Cost D={D} />}
            {prov && screen === 'metering' && <Metering D={D} />}
            {prov && screen === 'api' && <ApiSurface D={D} />}
            {!prov && tScreen === 't-overview' && <TOverview D={D} th={th} ledger={ledgerView} goTo={setTScreen} />}
            {!prov && tScreen === 't-models' && <TModels D={D} />}
            {!prov && tScreen === 't-usage' && <TUsage D={D} />}
            {!prov && tScreen === 't-billing' && <TBilling D={D} th={th} ledger={ledgerView} arOn={arOn} toggleAr={() => setArOn(!arOn)} />}
            {!prov && tScreen === 't-budgets' && <TBudgets D={D} />}
            {!prov && tScreen === 't-keys' && <TKeys D={D} keys={D.keys.concat(extraKeys)} createKey={createKey} secret={secret} dismiss={() => setSecret(null)} />}
          </div>
        </div>
      </div>
    </div>
  );
}
