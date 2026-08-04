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

export function fetchWalkthroughs() {
  return fetchJSON('/api/walkthroughs');
}

export function fetchWalkthrough(id) {
  return fetchJSON(`/api/walkthroughs/${encodeURIComponent(id)}`);
}

export function fetchWalkthroughStatus(id) {
  return fetchJSON(`/api/walkthroughs/${encodeURIComponent(id)}/status`);
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
