// All demo data for the MaaS FinOps console. cur is the currency symbol.
import { PAL } from './ui.jsx';

const cur = '$';
export const money = n => cur + n.toLocaleString('en-US');
const p2 = v => (v === null ? '—' : cur + v.toFixed(2));

export function makeData(inc = false) {
  const tenants = [
    { name: 'Kestrel Financial', plan: 'Enterprise', mode: 'Invoiced · Net-30', balN: null, reload: 'Invoiced monthly', burnN: 2807, mtdN: 84210, status: 'Active', st: 'green', tid: 'ten_kestrel' },
    { name: 'Helix Biotech', plan: 'Priority', mode: 'Prepaid', balN: 12480, reload: `At ${cur}5,000 → reload ${cur}25,000`, burnN: 2048, mtdN: 61447, status: 'Active', st: 'green', tid: 'ten_helix' },
    { name: 'Northwind Analytics', plan: 'Priority', mode: 'Prepaid', balN: 8112, reload: `At ${cur}2,000 → reload ${cur}10,000`, burnN: 1630, mtdN: 48902, status: 'Active', st: 'green', tid: 'ten_northwind' },
    { name: 'Osaka Robotics', plan: 'Standard', mode: 'Prepaid', balN: 942, reload: `At ${cur}1,000 → ${cur}5,000 (card declined)`, burnN: 1240, mtdN: 37215, status: 'Payment retry', st: 'gold', tid: 'ten_osaka' },
    { name: 'Brightline Health', plan: 'Standard', mode: 'Prepaid', balN: 6301, reload: `At ${cur}500 → reload ${cur}2,500`, burnN: 989, mtdN: 29660, status: 'Active', st: 'green', tid: 'ten_brightline' },
    { name: 'Tandem Labs', plan: 'Standard', mode: 'Prepaid', balN: 0, reload: 'Off', burnN: 0, mtdN: 8114, status: 'Frozen', st: 'red', tid: 'ten_tandem' },
    { name: 'Corteo Systems', plan: 'Standard', mode: 'Prepaid', balN: 1204, reload: `At ${cur}1,000 → reload ${cur}5,000`, burnN: 410, mtdN: 12377, status: 'Low balance', st: 'gold', tid: 'ten_corteo' },
    { name: 'Arrow & Vale', plan: 'Free', mode: 'Prepaid', balN: 25, reload: 'Off (trial credit)', burnN: 3, mtdN: 61, status: 'Trial', st: 'blue', tid: 'ten_arrowvale' }
  ].map(t => ({
    ...t,
    balance: t.balN === null ? '—' : money(t.balN),
    balColor: t.balN !== null && t.balN < 1300 ? '#B1380B' : '#151515',
    burn: money(t.burnN) + '/d', mtd: money(t.mtdN)
  }));

  const kpis = [
    { label: 'Usage revenue · MTD', value: money(412806), delta: '+18% vs June pace', deltaColor: '#204D00' },
    { label: 'Tokens served · MTD', value: '48.2B', delta: '31.4B in · 13.1B out · 2.9B reasoning', deltaColor: '#6a6e73' },
    { label: 'Active tenants', value: '62', delta: '4 low balance · 1 frozen · 3 trials', deltaColor: '#73480B' },
    { label: 'Gross margin', value: '61.4%', delta: 'hosted 66% · pass-through 12%', deltaColor: '#6a6e73' }
  ];

  const weeks = [212, 228, 224, 241, 255, 249, 262, 278, 290, 287, 301, 318]
    .map((v, i) => ({ h: Math.round(v / 318 * 100) + 'px', label: 'W' + (i + 27), top: cur + v + 'k', tip: `Week ${i + 27}: ${cur}${v},000` }));

  const tokenMix = [
    { label: 'Input', amt: '31.4B', rev: money(148300), w: '65%', color: '#0066CC', tip: 'Input 31.4B tokens' },
    { label: 'Output', amt: '13.1B', rev: money(201900), w: '27%', color: '#009596', tip: 'Output 13.1B tokens' },
    { label: 'Reasoning', amt: '2.9B', rev: money(52400), w: '6%', color: '#5752D1', tip: 'Reasoning 2.9B tokens' },
    { label: 'Cached reads', amt: '0.9B', rev: money(10200), w: '2%', color: '#F0AB00', tip: 'Cached 0.9B tokens' }
  ];

  const topModels = [
    { name: 'llama-4-maverick-fp8', rev: money(148210), tok: '21.3B' },
    { name: 'granite-4.0-h-small', rev: money(86420), tok: '14.8B' },
    { name: 'claude-sonnet-4.6 (PT)', rev: money(79105), tok: '4.1B' },
    { name: 'mistral-large-3', rev: money(61800), tok: '5.6B' },
    { name: 'qwen3-coder-480b', rev: money(37271), tok: '2.4B' }
  ];

  const alertsBase = [
    { color: '#B1380B', text: 'Tandem Labs balance depleted — keys frozen 09:14 UTC. Dunning email + webhook sent.' },
    { color: '#F0AB00', text: 'Osaka Robotics auto-reload failed (card_declined) · retry 2/4 at Jul 29 06:00.' },
    { color: '#F0AB00', text: 'Price book v3 effective Aug 1 — 4 tenants grandfathered on v2, notice sent Jul 1.' },
    { color: '#0066CC', text: 'qwen3-coder-480b margin 52%, below 60% target — review price or pool density.' }
  ];
  const alerts = inc
    ? [{ color: '#B1380B', text: 'Metering degraded: Loki ingest backlog 94s — balances may lag; invoicing paused until watermark recovers.' }, ...alertsBase]
    : alertsBase;

  const modelsRaw = [
    { name: 'llama-4-maverick-fp8', kind: 'Hosted · vLLM', geos: 'us-east-1 · eu-central-1', pin: .20, pout: .85, prsn: 1.20, pcache: .05, margin: '68%', status: 'GA', st: 'green', ver: 'v2' },
    { name: 'granite-4.0-h-small', kind: 'Hosted · vLLM', geos: 'us-east · eu-central · ap-south', pin: .10, pout: .38, prsn: null, pcache: .03, margin: '74%', status: 'GA', st: 'green', ver: 'v2' },
    { name: 'mistral-large-3', kind: 'Hosted · llm-d', geos: 'eu-central-1 only', pin: .55, pout: 1.90, prsn: 2.40, pcache: .14, margin: '57%', status: 'GA', st: 'green', ver: 'v2' },
    { name: 'qwen3-coder-480b-a35b', kind: 'Hosted · llm-d', geos: 'us-east-1', pin: .48, pout: 1.75, prsn: null, pcache: .12, margin: '52%', status: 'Preview', st: 'purple', ver: 'v1' },
    { name: 'claude-sonnet-4.6', kind: 'Pass-through · Anthropic', geos: 'global (provider)', pin: 3.36, pout: 16.80, prsn: 16.80, pcache: .42, margin: '12% markup', status: 'GA', st: 'green', ver: 'v2' },
    { name: 'gemini-2.5-pro', kind: 'Pass-through · Google', geos: 'global (provider)', pin: 1.40, pout: 11.20, prsn: 11.20, pcache: .35, margin: '12% markup', status: 'GA', st: 'green', ver: 'v2' }
  ];
  const models = modelsRaw.map(m => ({ ...m, pin: p2(m.pin), pout: p2(m.pout), prsn: p2(m.prsn), pcache: p2(m.pcache) }));
  const tModels = modelsRaw.map(m => {
    const disc = m.kind.indexOf('Hosted') === 0 ? 0.92 : 1;
    const f = v => (v === null ? '—' : cur + (v * disc).toFixed(2));
    return { name: m.name, kind: m.kind, geos: m.geos, pin: f(m.pin), pout: f(m.pout), prsn: f(m.prsn), pcache: f(m.pcache), note: disc < 1 ? '−8% volume tier' : 'provider list +12%' };
  });

  const geoRates = [
    { geo: 'us-east-1', pin: p2(.20), pout: p2(.85), prsn: p2(1.20), pcache: p2(.05), margin: '68%' },
    { geo: 'eu-central-1', pin: p2(.26), pout: p2(1.06), prsn: p2(1.44), pcache: p2(.06), margin: '63%' }
  ];
  const volTiers = [
    { band: '≥ 1B tokens / mo', disc: '−8%' },
    { band: '≥ 5B tokens / mo', disc: '−15%' },
    { band: '≥ 20B tokens / mo', disc: 'Custom (sales)' }
  ];
  const committed = [
    { band: 'Reserved 50 TPS', price: money(9400) + ' /mo' },
    { band: 'Reserved 200 TPS', price: money(34000) + ' /mo' }
  ];
  const priceVersions = [
    { tag: 'v3 draft', c: 'purple', text: 'Effective 2026-08-01 · output −12%, reasoning multiplier 1.4→1.3. Notice sent Jul 1.' },
    { tag: 'v2', c: 'blue', text: 'Current since 2026-03-15 · introduced cached-read rate and eu-central premium.' },
    { tag: 'v1', c: 'grey', text: 'Retired 2026-03-15 · flat in/out pricing, no reasoning split.' }
  ];

  const plans = [
    { name: 'Free', slug: 'free', rpm: '15', tpm: '20k', tpmN: 20000, day: '200k', dayN: 200000, conc: '2', tenantsN: 11, models: 'granite-4.0-h only', queue: 'Best-effort queue · trial credit ' + cur + '25' },
    { name: 'Standard', slug: 'standard', rpm: '120', tpm: '400k', tpmN: 400000, day: '40M', dayN: 40000000, conc: '16', tenantsN: 34, models: 'All GA hosted models', queue: 'Standard queue · prepaid + auto-reload' },
    { name: 'Priority', slug: 'priority', rpm: '600', tpm: '2M', tpmN: 2000000, day: '400M', dayN: 400000000, conc: '64', tenantsN: 14, models: 'GA hosted + pass-through', queue: 'Priority scheduling · volume tiers eligible' },
    { name: 'Enterprise', slug: 'enterprise', rpm: 'Custom', tpm: 'Custom', tpmN: 8000000, day: 'Custom', dayN: 2000000000, conc: 'Custom', tenantsN: 3, models: 'All + dedicated pools + committed TPS', queue: 'Invoiced · Net-30 · SLA 99.9%' }
  ];

  const enfLayers = [
    { layer: 'AuthPolicy', desc: 'Authorino validates the API key, resolves tenant, tier, model + geo scopes into auth.identity claims.' },
    { layer: 'RateLimitPolicy', desc: 'Request-rate and concurrency guardrails per tenant — protects the fleet from bursts.' },
    { layer: 'TokenRateLimitPolicy', desc: 'Token budgets per window; gateway reports actual usage as hits_addend after the response.' },
    { layer: 'Balance allowance', desc: 'Custom controller converts prepaid balance into short-window token grants; depletion freezes keys.' }
  ];

  const usageLegend = [
    { label: 'Input', color: '#0066CC' }, { label: 'Output', color: '#009596' },
    { label: 'Reasoning', color: '#5752D1' }, { label: 'Cached', color: '#F0AB00' }
  ];
  const days = Array.from({ length: 14 }, (_, i) => {
    const b = 2.4 + 0.5 * Math.sin(i / 1.9) + i * 0.09 + (i % 7 >= 5 ? -0.9 : 0);
    const seg = [b, b * .42, b * .1, b * .04];
    const k = 118 / 5.6;
    return { h1: Math.round(seg[0] * k) + 'px', h2: Math.round(seg[1] * k) + 'px', h3: Math.round(seg[2] * k) + 'px', h4: Math.round(seg[3] * k) + 'px', label: 'Jul ' + (15 + i), tip: `Jul ${15 + i}: ${(seg[0] + seg[1] + seg[2] + seg[3]).toFixed(1)}B tokens` };
  });

  const usageRows = [
    { model: 'llama-4-maverick-fp8', geo: 'us-east-1', req: '9.21M', tin: '11.2B', tout: '4.61B', trsn: '1.02B', tcache: '388M', cost: money(64112) },
    { model: 'llama-4-maverick-fp8', geo: 'eu-central-1', req: '4.02M', tin: '4.9B', tout: '2.05B', trsn: '441M', tcache: '141M', cost: money(35208) },
    { model: 'granite-4.0-h-small', geo: 'us-east-1', req: '11.8M', tin: '7.4B', tout: '2.61B', trsn: '—', tcache: '512M', cost: money(21470) },
    { model: 'granite-4.0-h-small', geo: 'ap-south-1', req: '3.1M', tin: '2.2B', tout: '790M', trsn: '—', tcache: '96M', cost: money(6180) },
    { model: 'mistral-large-3', geo: 'eu-central-1', req: '1.4M', tin: '2.6B', tout: '1.1B', trsn: '312M', tcache: '88M', cost: money(28904) },
    { model: 'claude-sonnet-4.6 (PT)', geo: 'provider', req: '512k', tin: '1.9B', tout: '820M', trsn: '196M', tcache: '402M', cost: money(38610) },
    { model: 'qwen3-coder-480b-a35b', geo: 'us-east-1', req: '890k', tin: '1.2B', tout: '640M', trsn: '—', tcache: '61M', cost: money(9905) }
  ].map(u => ({ ...u, total: false })).concat([{ model: 'Total', geo: '', req: '30.9M', tin: '31.4B', tout: '12.6B', trsn: '1.97B', tcache: '1.69B', cost: money(204389), total: true }]);

  const connectors = [
    { name: 'Stripe', status: 'Connected', st: 'green', top: '#635BFF', desc: 'Payments + Link for auto-reload · Stripe Tax · usage-record sync every 5 min · credit grants API.', action: 'Configure' },
    { name: 'Adyen', status: 'Available', st: 'grey', top: '#d2d2d2', desc: 'Card + local payment methods for EU tenants; alternative PSP for geo redundancy.', action: 'Connect' },
    { name: 'Zuora', status: 'Available', st: 'grey', top: '#d2d2d2', desc: 'Enterprise invoicing, rev-rec (ASC 606) export for prepaid credit liability.', action: 'Connect' },
    { name: 'NetSuite', status: 'Export only', st: 'blue', top: '#8a8d90', desc: 'GL journal export — nightly. Deferred revenue + recognized usage postings.', action: 'View mapping' }
  ];

  const invoiceRuns = [
    { period: 'Jul 2026', count: '—', amount: money(412806) + ' accrued', status: 'Accruing', st: 'blue', action: 'Preview' },
    { period: 'Jun 2026', count: '62', amount: money(1291400), status: 'Settled', st: 'green', action: 'View' },
    { period: 'May 2026', count: '58', amount: money(1104980), status: 'Settled', st: 'green', action: 'View' }
  ];

  const dunningBase = [
    { tenant: 'Osaka Robotics', amount: money(5000), detail: 'Auto-reload declined (card_declined) · attempt 2 of 4 · next retry Jul 29 06:00 UTC · grace: keys active', a1: 'Retry now', a2: 'Pause keys' },
    { tenant: 'Tandem Labs', amount: money(612), detail: 'Final invoice unpaid 12 days · keys frozen · escalation email sent to billing contact', a1: 'Send reminder', a2: 'Write off' }
  ];

  const grants = [
    { text: 'SLA credit — eu-central degradation Jul 12', amt: money(1840), when: 'Jul 14' },
    { text: 'Promo — Granite launch, 12 tenants', amt: money(6000), when: 'Jul 02' },
    { text: 'Goodwill — Brightline onboarding issue', amt: money(250), when: 'Jun 28' }
  ];

  const webhookLog = [
    { event: 'payment.failed', dest: 'osaka · hooks.osakarobotics.com', code: '200', st: 'green', when: '06:00' },
    { event: 'balance.depleted', dest: 'tandem · ops.tandemlabs.io', code: '200', st: 'green', when: '09:14' },
    { event: 'balance.low', dest: 'corteo · alerts.corteo.dev', code: '200', st: 'green', when: '08:41' },
    { event: 'invoice.finalized', dest: 'kestrel · erp.kestrel.com', code: '500 · retry', st: 'red', when: '07:02' }
  ];

  const costKpis = [
    { label: 'Fleet cost / day', value: money(14210), delta: '4 pools · amortized + power' },
    { label: 'Usage revenue / day', value: money(23480), delta: 'trailing 7-day mean' },
    { label: 'Pass-through spend MTD', value: money(103400), delta: 'billed ' + money(115800) + ' (+12%)' },
    { label: 'Gross margin', value: '61.4%', delta: 'target 60% · hosted-only 66%' }
  ];

  const modelMargins = [
    { name: 'granite-4.0-h-small', rev: money(3120), cost: money(811), margin: '74%', mColor: '#3D7317', w: '74%' },
    { name: 'llama-4-maverick-fp8', rev: money(5340), cost: money(1709), margin: '68%', mColor: '#3D7317', w: '68%' },
    { name: 'mistral-large-3', rev: money(2230), cost: money(959), margin: '57%', mColor: '#F0AB00', w: '57%' },
    { name: 'qwen3-coder-480b-a35b', rev: money(1340), cost: money(643), margin: '52%', mColor: '#F0AB00', w: '52%' }
  ];

  const pools = [
    { name: 'h200-us-a', gpus: '48× H200', region: 'us-east-1', util: '82%', uColor: '#3D7317', cost: money(4838) },
    { name: 'h200-eu-a', gpus: '32× H200', region: 'eu-central-1', util: '71%', uColor: '#3D7317', cost: money(3661) },
    { name: 'mi300-us-b', gpus: '24× MI300X', region: 'us-east-1', util: '64%', uColor: '#F0AB00', cost: money(2196) },
    { name: 'l40s-ap-a', gpus: '40× L40S', region: 'ap-south-1', util: '38%', uColor: '#B1380B', cost: money(1310) }
  ];

  const passThrough = [
    { name: 'Anthropic', spend: money(78200), billed: money(87580), margin: '+12%' },
    { name: 'Google', spend: money(25200), billed: money(28220), margin: '+12%' }
  ];

  const stg = (name, metric, sub, ok) => ({ name, metric, sub, color: ok ? '#3D7317' : '#B1380B', vColor: ok ? '#151515' : '#B1380B' });
  const stages = [
    stg('Gateway (Envoy ext-proc)', '932k ev/hr', 'per-request usage events · 0 drops', true),
    stg('Vector shippers', '99.98% ack', 'buffered, at-least-once', true),
    inc ? stg('Loki', 'lag 94s', 'ingest backlog — compactor OOM', false) : stg('Loki', 'lag 4.2s', '90-day retention · audit replay', true),
    inc ? stg('Ledger (ClickHouse)', 'watermark −22m', 'dedup 0.31% · catching up', false) : stg('Ledger (ClickHouse)', 'dedup 0.31%', 'watermark 09:41:22Z', true),
    inc ? stg('Rating engine', 'lag 22m', 'balances stale — reloads paused', false) : stg('Rating engine', 'lag 41s', 'price book v2 · 4 grandfathered', true)
  ];

  const meterKpis = inc ? [
    { label: 'Unmetered requests · 24h', value: '1.8%', delta: 'reconciliation job queued', border: '#B1380B' },
    { label: 'Reconciliation drift', value: '−2.4%', delta: 'gateway vs engine-reported', border: '#B1380B' },
    { label: 'Dead-letter queue', value: '12,406', delta: 'events · replay after backlog', border: '#F0AB00' },
    { label: 'Stream disconnects · 24h', value: '1,892', delta: '96% billed at disconnect point', border: '#3D7317' }
  ] : [
    { label: 'Unmetered requests · 24h', value: '0.04%', delta: 'all reconciled within 15 min', border: '#3D7317' },
    { label: 'Reconciliation drift', value: '−0.12%', delta: 'gateway vs engine-reported', border: '#3D7317' },
    { label: 'Dead-letter queue', value: '214', delta: 'malformed usage objects · replayable', border: '#F0AB00' },
    { label: 'Stream disconnects · 24h', value: '1,892', delta: '96% billed at disconnect point', border: '#3D7317' }
  ];

  const recon = [
    { day: 'Jul 27', model: 'llama-4-maverick-fp8', gw: '1.42B', eng: '1.42B', drift: '−0.08%', ok: true },
    { day: 'Jul 27', model: 'granite-4.0-h-small', gw: '980M', eng: '981M', drift: '+0.05%', ok: true },
    { day: 'Jul 26', model: 'mistral-large-3', gw: '312M', eng: '313M', drift: '−0.41%', ok: false },
    { day: 'Jul 26', model: 'claude-sonnet-4.6 (PT)', gw: '241M', eng: '241M', drift: '−0.02%', ok: true },
    { day: 'Jul 25', model: 'qwen3-coder-480b-a35b', gw: '188M', eng: '188M', drift: '+0.09%', ok: true }
  ];

  const edgeCases = [
    { label: 'Client disconnects mid-stream', value: '1,892' },
    { label: 'Retried requests deduplicated', value: '3,411' },
    { label: 'Late events (>5 min) reconciled', value: '406' },
    { label: 'Missing usage object (engine err)', value: '38' },
    { label: 'Balance-overrun writeoffs', value: money(41) }
  ];

  const ledger = [
    { date: 'Jul 28 09:00', type: 'Usage', detail: '38.1M tokens · llama-4 + granite · rolled up hourly', amt: '−' + cur + '612.40', after: money(12480), c: 'grey', neg: true },
    { date: 'Jul 27', type: 'Usage', detail: '124.6M tokens across 3 keys', amt: '−' + cur + '2,048.11', after: money(13092), c: 'grey', neg: true },
    { date: 'Jul 24', type: 'Reload', detail: 'Auto-reload · Stripe Link •••• 8842 · pi_3Qx8…', amt: '+' + cur + '25,000.00', after: money(19341), c: 'green' },
    { date: 'Jul 24', type: 'Usage', detail: '119.2M tokens', amt: '−' + cur + '1,982.77', after: cur + '−5,659 → 0 (grace)', c: 'grey', neg: true },
    { date: 'Jul 12', type: 'Credit', detail: 'SLA credit — eu-central degradation', amt: '+' + cur + '1,840.00', after: money(8114), c: 'blue' },
    { date: 'Jul 01', type: 'Reload', detail: 'Auto-reload · Stripe Link •••• 8842', amt: '+' + cur + '25,000.00', after: money(29377), c: 'green' }
  ].map(l => ({ ...l, amtColor: l.neg ? '#4d4d4d' : '#204D00' }));

  const budgets = [
    { name: 'prod-rag-pipeline', used: money(21412), cap: money(30000), w: '71%', color: '#0066CC', note: 'On pace · resets Aug 1' },
    { name: 'research-agents', used: money(13904), cap: money(15000), w: '93%', color: '#F0AB00', note: '80% alert fired Jul 22 · soft cap — overage allowed' },
    { name: 'eval-nightly', used: money(2131), cap: money(5000), w: '43%', color: '#0066CC', note: 'Hard cap — keys throttle at 100%' }
  ];

  const alertRules = [
    { when: 'Budget ≥ 80% (any project)', then: 'Email finance-ai@ + webhook budget.threshold' },
    { when: `Balance < ${cur}5,000`, then: 'Webhook balance.low → tenant ops channel' },
    { when: 'Spend velocity > 3× 7-day baseline', then: 'Freeze non-prod keys + page on-call (anomaly guard)' },
    { when: 'Auto-reload failure', then: 'Email billing contact + retry schedule 4× exponential' }
  ];

  const keys = [
    { name: 'prod-rag', prefix: 'maas_sk_9f3a…0c2e', scopes: 'llama-4, granite-4 · us-east-1', limit: '400k TPM', last: '2 min ago', status: 'Active', st: 'green' },
    { name: 'research-agents', prefix: 'maas_sk_77b1…e9d4', scopes: 'claude-sonnet (PT), llama-4 · any geo', limit: '100k TPM', last: '14 min ago', status: 'Active', st: 'green' },
    { name: 'ci-eval', prefix: 'maas_sk_2c48…a1f7', scopes: 'granite-4 · us-east-1', limit: '50k TPM', last: '3 hr ago', status: 'Active', st: 'green' },
    { name: 'legacy-poc', prefix: 'maas_sk_e01d…4b88', scopes: 'mistral-large · eu-central-1', limit: '—', last: '62 days ago', status: 'Disabled', st: 'grey' }
  ];

  const chargeback = [
    { cc: 'RND-114', project: 'prod-rag-pipeline', tin: '2.41B', tout: '890M', trsn: '204M', cost: money(21412), w: '57%' },
    { cc: 'RND-114', project: 'research-agents', tin: '1.02B', tout: '512M', trsn: '388M', cost: money(13904), w: '37%' },
    { cc: 'PLT-201', project: 'eval-nightly', tin: '388M', tout: '96M', trsn: '—', cost: money(2131), w: '6%' }
  ];

  const apiHeaders = [
    { name: 'x-maas-request-id', desc: 'Join key into the usage ledger — quote it in billing disputes.' },
    { name: 'x-ratelimit-remaining-tokens', desc: 'Tokens left in the current window (plan-level).' },
    { name: 'x-ratelimit-remaining-requests', desc: 'Requests left in the current window.' },
    { name: 'x-maas-balance-remaining', desc: 'Prepaid balance after this call · eventual (≤60s).' },
    { name: 'x-maas-price-version', desc: 'Price book applied to this request (e.g. v2).' }
  ];

  const apiMgmt = [
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

  const webhookTypes = ['balance.low', 'balance.depleted', 'limit.exceeded', 'budget.threshold', 'invoice.finalized', 'payment.failed', 'price.changed', 'key.rotated', 'usage.anomaly'];

  const controlPlane = [
    { cr: 'AuthPolicy', desc: 'API key → identity claims (tenant, tier, scopes) via Authorino.' },
    { cr: 'TokenRateLimitPolicy', desc: 'Token windows per tier/tenant — Limitador counters, Redis-backed cross-cluster.' },
    { cr: 'RateLimitPolicy', desc: 'Request-rate + concurrency guardrails.' },
    { cr: 'LLMInferenceService', desc: 'Model serving (KServe / llm-d) per geo — the SKU\u2019s supply side.' },
    { cr: 'BalanceAllowance (custom)', desc: 'Prepaid balance → rolling short-window token grants; the piece RHOAI does not ship today.' }
  ];

  const tKeyUsage = [
    { key: 'prod-rag', model: 'llama-4-maverick-fp8', req: '6.1M', tin: '3.1B', tout: '1.4B', trsn: '204M', cost: money(38204) },
    { key: 'research-agents', model: 'claude-sonnet-4.6 (PT)', req: '312k', tin: '890M', tout: '402M', trsn: '184M', cost: money(18911) },
    { key: 'ci-eval', model: 'granite-4.0-h-small', req: '2.4M', tin: '610M', tout: '240M', trsn: '—', cost: money(4332) }
  ];

  const receipts = [
    { label: 'Auto-reload · Jul 24', amt: money(25000) },
    { label: 'Auto-reload · Jul 01', amt: money(25000) },
    { label: 'Auto-reload · Jun 12', amt: money(25000) }
  ];

  return {
    cur, tenants, kpis, weeks, tokenMix, topModels, alerts, models, tModels, geoRates, volTiers, committed,
    priceVersions, plans, enfLayers, usageLegend, days, usageRows, connectors, invoiceRuns, dunningBase,
    grants, webhookLog, costKpis, modelMargins, pools, passThrough, stages, meterKpis, recon, edgeCases,
    ledger, budgets, alertRules, keys, chargeback, apiHeaders, apiMgmt, webhookTypes, controlPlane,
    tKeyUsage, receipts,
    pipeColor: inc ? '#B1380B' : '#3D7317',
    pipeSummary: inc ? 'Degraded — Loki ingest backlog 94s; balances lag, invoicing paused.' : 'Healthy — ingest lag 4.2s · drift −0.12% · 0.04% unmetered (reconciled).'
  };
}

export function mkDetail(t0, credited, frozen) {
  const crd = credited[t0.tid] ? 1000 : 0;
  const t = crd && t0.balN !== null ? { ...t0, balN: t0.balN + crd, balance: money(t0.balN + crd) } : t0;
  const runwayD = t.balN && t.burnN ? (t.balN / t.burnN).toFixed(1) : null;
  const d = {
    ...t,
    runway: t.balN === null ? 'Invoiced tenant — no prepaid balance' : t.balN === 0 ? 'Depleted — keys frozen at 09:14 UTC' : `≈ ${runwayD} days of runway at ${money(t.burnN)}/day burn`,
    gaugeW: t.balN === null ? '100%' : Math.min(100, Math.round((t.balN / 25000) * 100)) + '%',
    gaugeColor: t.balN !== null && t.balN < 1300 ? '#B1380B' : t.balN !== null && t.balN < 5000 ? '#F0AB00' : '#3D7317',
    arState: t.status === 'Payment retry' ? 'Retrying 2/4' : t.reload.indexOf('Off') === 0 ? 'Off' : t.balN === null ? 'N/A (invoiced)' : 'Enabled',
    arWarn: t.status === 'Payment retry',
    arThreshold: t.balN === null ? '—' : `${cur}5,000`, arAmount: t.balN === null ? '—' : `${cur}25,000`,
    arMethod: t.balN === null ? 'Invoice · PO #8841' : 'Stripe · Link •••• 8842',
    arCap: `${cur}60,000 / mo`,
    arLast: t.status === 'Payment retry'
      ? 'Last attempt failed (card_declined) · next retry Jul 29 06:00 UTC · webhook payment.failed delivered'
      : `Last reload Jul 24 · ${cur}25,000 · succeeded in 1.9s via Link`,
    overrun: t.burnN ? `≈ ${money(Math.round(t.burnN / 288))}` : `${cur}0`
  };
  if (frozen[t0.tid]) { d.status = 'Frozen'; d.st = 'red'; }
  return d;
}

export function planYaml(sel) {
  return `apiVersion: kuadrant.io/v1alpha1
kind: TokenRateLimitPolicy
metadata:
  name: plan-${sel.slug}
  namespace: maas-gateway
spec:
  targetRef:
    group: gateway.networking.k8s.io
    kind: Gateway
    name: maas-default-gateway
  limits:
    ${sel.slug}-tpm:
      rates:
        - limit: ${sel.tpmN}        # tokens / minute
          window: 1m
      when:
        - predicate: auth.identity.tier == "${sel.slug}"
      counters:
        - expression: auth.identity.tenant_id
    ${sel.slug}-daily:
      rates:
        - limit: ${sel.dayN}   # tokens / day
          window: 24h
      when:
        - predicate: auth.identity.tier == "${sel.slug}"
      counters:
        - expression: auth.identity.tenant_id
---
# Companion CRs generated with this plan:
#  RateLimitPolicy   — ${sel.rpm} req/min, concurrency ${sel.conc}
#  AuthPolicy        — API key → {tier, tenant_id,
#                      allowed_models, geo_scope}
#  BalanceAllowance  — rolling 5m token grant from
#                      prepaid balance (custom controller)`;
}

export const jsonUsage = `"usage": {
  "prompt_tokens": 1841,
  "completion_tokens": 512,
  "total_tokens": 2353,
  "prompt_tokens_details":     { "cached_tokens": 1024 },
  "completion_tokens_details": { "reasoning_tokens": 128 }
}`;

export const jsonEvent = `POST /internal/v1/usage-events
{
  "request_id": "req_9f3a2c",        // idempotency key
  "tenant_id":  "ten_helix",
  "key_id":     "key_prod_rag",
  "model":      "llama-4-maverick-fp8",
  "geo":        "us-east-1",
  "prompt_tokens":     1841,
  "completion_tokens": 512,
  "reasoning_tokens":  128,
  "cached_tokens":     1024,
  "status": "ok",     // ok | client_disconnect | error
  "stream": true,
  "latency_ms": 2140,
  "price_version": "v2",
  "ts": "2026-07-28T09:41:22Z"
}`;

export const jsonWebhook = `{
  "type": "balance.low",
  "tenant_id": "ten_osaka",
  "balance": 942.10,
  "threshold": 1000.00,
  "auto_reload": { "state": "retrying", "attempt": 2 },
  "ts": "2026-07-28T06:00:04Z"
}`;
