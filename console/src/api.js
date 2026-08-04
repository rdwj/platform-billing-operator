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
