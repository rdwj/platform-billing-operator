# Session Summary: G2 Phase 5 — Console Reservation Screens

**Date:** 2026-08-05
**Epic:** g2-gpu-reservations
**Phase:** 5 of 6
**Commit:** 9729e82

## What landed

Console reservation screens for both provider and tenant personas.
Four files changed, 469 insertions.

### API layer (api.js)

- Added `postJSON(url, body)` helper for POST requests with error handling
- `fetchReservations(tenant?)` — GET, optional tenant filter
- `createReservation(body)` — POST to /api/reservations
- `cancelReservation(name)` — POST to /api/reservations/{name}/cancel
- BFF proxies all HTTP methods with `/api/*` → `/v1/*` rewrite, so no
  Go changes were needed

### Tenant portal (screens-tenant.jsx)

`TReservations` component added with:
- Reservation table with 9 columns (Name, SKU, GPUs, Geo, Class, Window,
  Price/mo, State, actions)
- State badges: Active=green, Confirmed=blue, Cancelled=grey, Expired=gold
- Cancel button with window.confirm() guard (only for Active/Confirmed)
- Toggle-able create form: SKU, count, geo, class dropdown
  (Guaranteed/Preemptible), date range, monthly price
- Form resets on submit, closes on cancel
- Wired as "GPU > Reservations" nav section in tenant portal

### Provider capacity calendar (screens-reservations.jsx — new file)

`CapacityCalendar` component (303 lines) with:
- KPI row: total reservations, active GPUs, Guaranteed/Preemptible split
- Timeline/gantt view: reservations grouped by SKU+geo
- Month column headers derived from data range
- Horizontal bars positioned by percentage within timeline span
- Lane stacking for overlapping reservations in the same group
- Color coding: Guaranteed=#0066CC (blue), Preemptible=#009596 (teal)
- State opacity: Cancelled=30% with strikethrough, Expired=40%
- Hover tooltips with full reservation details
- Legend for class colors
- Empty state handling
- Wired as "GPU reservations" under Operate in provider nav

### App.jsx wiring

- Imports for new API functions and components
- `apiReservations` and `apiPortalReservations` state
- Fetch on mount (provider: all) and on tenant persona switch (per-tenant)
- Create/cancel handlers that refresh both provider and portal data
- Nav entries for both personas
- Screen rendering branches

## Verification

- Vite build: 39 modules, 0 errors
- Browser testing: both screens render, nav highlights correctly,
  empty states display properly, no JS console errors
- Existing screens: no regressions (spot-checked Integrations, Overview)
- Network: /api/reservations called correctly (500 expected without backend)

## Deferred

- Utilization-vs-reservation overlay (stretch goal — needs combined
  reservation + usage data)
- Queue/quota display (stretch goal — needs new BFF endpoint for Kueue
  ClusterQueue status)

## What's next

Phase 6: E2E demo script (`hack/demo-g2.sh`) + milestone close. This is
the final G2 phase.
