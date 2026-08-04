import React, { useEffect, useMemo, useState } from 'react';
import { makeData, mkDetail } from './data.js';
import { DISPLAY, PAL } from './ui.jsx';
import { Overview, Models, Tenants, TenantDetail, Plans, UsageExplorer, Billing, Cost, Metering, ApiSurface } from './screens-provider.jsx';
import { TOverview, TModels, TUsage, TBilling, TBudgets, TKeys } from './screens-tenant.jsx';
import { fetchTenants, fetchBalance } from './api.js';

const PROVIDER_NAV = [
  { hdr: 'Monetize' },
  { id: 'overview', label: 'Overview' }, { id: 'models', label: 'Models & pricing' }, { id: 'plans', label: 'Plans & rate limits' },
  { hdr: 'Customers' },
  { id: 'tenants', label: 'Tenants' }, { id: 'usage', label: 'Usage explorer' },
  { hdr: 'Operate' },
  { id: 'billing', label: 'Billing operations' }, { id: 'cost', label: 'Cost & margin' }, { id: 'metering', label: 'Metering health' },
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
  const [apiBalance, setApiBalance] = useState(null);

  // Fetch tenant list from BFF on mount
  useEffect(() => {
    fetchTenants().then(data => {
      if (data && data.tenants) setApiTenants(data.tenants);
    });
  }, []);

  // Fetch balance when selected tenant changes
  useEffect(() => {
    if (!tenant) { setApiBalance(null); return; }
    fetchBalance(tenant.tid).then(data => {
      if (data) setApiBalance(data);
    });
  }, [tenant?.tid]);

  const D = useMemo(() => {
    const mock = makeData(inc);
    // If BFF returned tenant data, attach it so screens can use it.
    // Components still consume `D` with the same shape -- apiTenants is
    // an additive field that screens can inspect when they're ready.
    if (apiTenants) {
      mock._apiTenants = apiTenants;
    }
    if (apiBalance) {
      mock._apiBalance = apiBalance;
    }
    return mock;
  }, [inc, apiTenants, apiBalance]);

  const prov = persona === 'provider';
  const nav = prov ? PROVIDER_NAV : TENANT_NAV;
  const active = prov ? screen : tScreen;

  const td = mkDetail(tenant ? D.tenants.find(x => x.tid === tenant.tid) : D.tenants[1], credited, frozen);
  const th = mkDetail(D.tenants[1], credited, frozen);
  const ledgerView = (credited[td.tid]
    ? [{ date: 'Just now', type: 'Credit', detail: 'Goodwill credit granted from provider console', amt: '+$1,000.00', after: td.balance, c: 'blue', amtColor: '#204D00' }]
    : []).concat(D.ledger);

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
            {prov && screen === 'overview' && <Overview D={D} onTenant={openTenant} goMetering={() => setScreen('metering')} />}
            {prov && screen === 'models' && <Models D={D} />}
            {prov && screen === 'plans' && <Plans D={D} plan={plan} setPlan={setPlan} />}
            {prov && screen === 'tenants' && !tenant && <Tenants D={D} onTenant={openTenant} />}
            {prov && screen === 'tenants' && tenant && (
              <TenantDetail D={D} td={td} tab={tab} setTab={setTab} ledger={ledgerView}
                back={() => setTenant(null)} goPlans={() => { setScreen('plans'); setTenant(null); }}
                grantCredit={() => !credited[td.tid] && setCredited({ ...credited, [td.tid]: true })}
                grantLabel={credited[td.tid] ? 'Credited +$1,000' : 'Grant credit'}
                toggleFreeze={() => setFrozen({ ...frozen, [td.tid]: !frozen[td.tid] })}
                freezeLabel={frozen[td.tid] ? 'Unfreeze keys' : 'Freeze keys'} />
            )}
            {prov && screen === 'usage' && <UsageExplorer D={D} />}
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
