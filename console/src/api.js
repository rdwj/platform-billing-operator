// Fetch functions for BFF API endpoints.
// Each returns parsed JSON or null on error.

async function fetchJSON(url) {
  try {
    const res = await fetch(url);
    if (!res.ok) return null;
    return await res.json();
  } catch {
    return null;
  }
}

async function postJSON(url, body) {
  try {
    const res = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    if (!res.ok) {
      const text = await res.text();
      return { error: text || res.statusText };
    }
    return await res.json();
  } catch (e) {
    return { error: e.message };
  }
}

export function fetchTenants() {
  return fetchJSON('/api/tenants');
}

export function fetchBalance(tenantId) {
  return fetchJSON(`/api/balance?tenant=${encodeURIComponent(tenantId)}`);
}

export function fetchUsage(params) {
  const qs = new URLSearchParams(params).toString();
  return fetchJSON(`/api/usage?${qs}`);
}

export function fetchInvoices(tenantId) {
  return fetchJSON(`/api/invoices?tenant=${encodeURIComponent(tenantId)}`);
}

export function fetchDrift(tenantId) {
  return fetchJSON(`/api/drift?tenant=${encodeURIComponent(tenantId)}`);
}

export function fetchPricing(tenantId) {
  return fetchJSON(`/api/pricing?tenant=${encodeURIComponent(tenantId)}`);
}

export function fetchExport(params) {
  const qs = new URLSearchParams(params).toString();
  return fetchJSON(`/api/export?${qs}`);
}

export function fetchLedger(tenantId, limit = 50) {
  return fetchJSON(`/api/ledger?tenant=${encodeURIComponent(tenantId)}&limit=${limit}`);
}

export function fetchBudgets(tenantId) {
  return fetchJSON(`/api/budgets?tenant=${encodeURIComponent(tenantId)}`);
}

export function fetchKeys(tenantId) {
  return fetchJSON(`/api/keys?tenant=${encodeURIComponent(tenantId)}`);
}

export function fetchAdjustments(tenantId, limit = 50) {
  return fetchJSON(`/api/adjustments?tenant=${encodeURIComponent(tenantId)}&limit=${limit}`);
}

export function fetchPlans() {
  return fetchJSON('/api/plans');
}

export function fetchPriceBooks() {
  return fetchJSON('/api/pricebooks');
}

export function fetchOverview() {
  return fetchJSON('/api/overview');
}

export function fetchConnectors() {
  return fetchJSON('/api/connectors');
}

export function fetchWebhooks(limit = 20) {
  return fetchJSON(`/api/webhooks?limit=${limit}`);
}

export function fetchInvoiceRuns() {
  return fetchJSON('/api/invoices');
}

export function fetchAdjustmentsAll(kind, limit = 20) {
  const params = new URLSearchParams({ limit: String(limit) });
  if (kind) params.set('kind', kind);
  return fetchJSON(`/api/adjustments?${params}`);
}

export function fetchWalkthroughs() {
  return fetchJSON('/api/walkthroughs');
}

export function fetchWalkthrough(id) {
  return fetchJSON(`/api/walkthroughs/${encodeURIComponent(id)}`);
}

export function fetchWalkthroughStatus(id) {
  return fetchJSON(`/api/walkthroughs/${encodeURIComponent(id)}/status`);
}

export function fetchHealth() {
  return fetchJSON('/api/health');
}

export function fetchDriftAll(limit = 20) {
  return fetchJSON(`/api/drift?limit=${limit}`);
}

export function fetchReservations(tenant) {
  const params = new URLSearchParams();
  if (tenant) params.set('tenant', tenant);
  const qs = params.toString();
  return fetchJSON(`/api/reservations${qs ? '?' + qs : ''}`);
}

export function createReservation(body) {
  return postJSON('/api/reservations', body);
}

export function cancelReservation(name) {
  return postJSON(`/api/reservations/${encodeURIComponent(name)}/cancel`, {});
}

export async function executeStep(walkthroughId, stepId, formValues) {
  try {
    const res = await fetch(`/api/walkthroughs/${encodeURIComponent(walkthroughId)}/steps/${encodeURIComponent(stepId)}/execute`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ formValues }),
    });
    if (!res.ok) {
      const text = await res.text();
      return { error: text };
    }
    return await res.json();
  } catch (e) {
    return { error: e.message };
  }
}
