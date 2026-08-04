import React, { useEffect, useState } from 'react';
import { Card, PageH, Badge, Btn, SecTitle, PAL, DISPLAY, MONO } from './ui.jsx';
import { fetchWalkthroughs, fetchWalkthrough, fetchWalkthroughStatus, executeStep } from './api.js';

// --- Status helpers ---

function statusBadge(status, reason) {
  if (status === 'complete') return <Badge c="green">Complete</Badge>;
  if (status === 'failed') return <Badge c="red">{reason || 'Failed'}</Badge>;
  if (status === 'pending') return <Badge c="grey">Pending</Badge>;
  return <Badge c="grey">Not evaluated</Badge>;
}

function summarize(statuses, stepCount) {
  if (!statuses || statuses.length === 0) return { label: 'Not started', color: 'grey' };
  const done = statuses.filter(s => s.status === 'complete').length;
  if (done === stepCount) return { label: 'Complete', color: 'green' };
  if (done > 0) return { label: `In progress (${done}/${stepCount} steps)`, color: 'gold' };
  const any = statuses.some(s => s.status === 'failed' || s.status === 'pending');
  if (any) return { label: 'Not started', color: 'grey' };
  return { label: 'Not started', color: 'grey' };
}

// --- Form field component ---

function FormField({ field, value, onChange }) {
  const isSecret = field.kind === 'secret';
  return (
    <div style={{ marginBottom: 12 }}>
      <label style={{ display: 'block', fontSize: 13, fontWeight: 600, marginBottom: 4 }}>{field.label}</label>
      {field.help && <div style={{ fontSize: 12, color: '#6a6e73', marginBottom: 4 }}>{field.help}</div>}
      <input
        type={isSecret ? 'password' : 'text'}
        value={value || ''}
        onChange={e => onChange(field.name, e.target.value)}
        placeholder={field.placeholder || ''}
        style={{
          width: '100%', padding: '8px 12px', border: '1px solid #d2d2d2', borderRadius: 6,
          fontFamily: "'Red Hat Text',sans-serif", fontSize: 13, boxSizing: 'border-box',
        }}
      />
    </div>
  );
}

// --- Display value component ---

function DisplayValue({ item, formValues, onFormChange }) {
  if (item.form) {
    return <FormField field={item.form} value={formValues[item.form.name]} onChange={onFormChange} />;
  }
  return (
    <div style={{ marginBottom: 10 }}>
      <div style={{ fontSize: 12, fontWeight: 600, color: '#6a6e73', marginBottom: 2 }}>{item.label}</div>
      <div style={{ fontSize: 13 }}>
        {item.value || (item.from
          ? <span style={{ color: '#8a8d90', fontStyle: 'italic' }}>Populated from connector status</span>
          : <span style={{ color: '#8a8d90' }}>--</span>)}
      </div>
    </div>
  );
}

// --- Step component ---

function StepItem({ step, stepIndex, stepStatus, walkthroughId, formValues, setFormValues, onExecuted }) {
  const [executing, setExecuting] = useState(false);
  const [result, setResult] = useState(null);

  const status = stepStatus?.status;
  const isComplete = status === 'complete';
  const circleColor = isComplete ? '#0066CC' : '#e0e0e0';
  const circleText = isComplete ? '#fff' : '#6a6e73';

  const hasForm = step.form && step.form.length > 0;
  const hasWrites = step.writes && step.writes.length > 0;
  const hasDisplay = step.display && step.display.length > 0;

  const handleFieldChange = (name, value) => {
    setFormValues(prev => ({ ...prev, [name]: value }));
  };

  const handleExecute = async () => {
    setExecuting(true);
    setResult(null);
    const res = await executeStep(walkthroughId, step.id, formValues);
    setResult(res);
    setExecuting(false);
    if (!res.error) onExecuted();
  };

  return (
    <div style={{ display: 'flex', gap: 14, position: 'relative' }}>
      {/* Vertical connector line */}
      <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', flexShrink: 0, width: 32 }}>
        <div style={{
          width: 32, height: 32, borderRadius: '50%', background: circleColor, color: circleText,
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          fontSize: 13, fontWeight: 700, flexShrink: 0,
        }}>
          {stepIndex + 1}
        </div>
        <div style={{ flex: 1, width: 2, background: '#e0e0e0', marginTop: 4 }} />
      </div>
      {/* Step content */}
      <div style={{ flex: 1, paddingBottom: 24 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 8 }}>
          <div style={{ fontFamily: DISPLAY, fontSize: 15, fontWeight: 600 }}>{step.title}</div>
          {stepStatus && statusBadge(stepStatus.status, stepStatus.reason)}
        </div>
        {step.text && <div style={{ fontSize: 13, color: '#4d4d4d', marginBottom: 12, lineHeight: 1.5 }}>{step.text}</div>}
        {hasDisplay && (
          <div style={{ marginBottom: 12 }}>
            {step.display.map((item, i) => (
              <DisplayValue key={i} item={item} formValues={formValues} onFormChange={handleFieldChange} />
            ))}
          </div>
        )}
        {hasForm && (
          <div style={{ marginBottom: 12 }}>
            {step.form.map(field => (
              <FormField key={field.name} field={field} value={formValues[field.name]} onChange={handleFieldChange} />
            ))}
          </div>
        )}
        {hasWrites && (
          <Btn primary onClick={handleExecute} style={{ opacity: executing ? 0.6 : 1 }}>
            {executing ? 'Applying...' : (step.executeLabel || 'Apply')}
          </Btn>
        )}
        {result && !result.error && (
          <div style={{
            marginTop: 10, padding: '10px 14px', background: PAL.green.bg, border: `1px solid ${PAL.green.fg}33`,
            borderRadius: 6, fontSize: 12, color: PAL.green.fg,
          }}>
            Created: {result.written?.map(r => `${r.kind}/${r.name}`).join(', ') || 'Resources applied'}
          </div>
        )}
        {result?.error && (
          <div style={{
            marginTop: 10, padding: '10px 14px', background: PAL.red.bg, border: `1px solid ${PAL.red.fg}33`,
            borderRadius: 6, fontSize: 12, color: PAL.red.fg,
          }}>
            {result.error}
          </div>
        )}
        {stepStatus?.message && (
          <div style={{ marginTop: 8, fontSize: 12, color: '#6a6e73', fontStyle: 'italic' }}>{stepStatus.message}</div>
        )}
      </div>
    </div>
  );
}

// --- Wizard drawer ---

function WalkthroughWizard({ walkthroughId, onClose }) {
  const [definition, setDefinition] = useState(null);
  const [statuses, setStatuses] = useState([]);
  const [formValues, setFormValues] = useState({});
  const [loading, setLoading] = useState(true);

  const load = () => {
    Promise.all([
      fetchWalkthrough(walkthroughId),
      fetchWalkthroughStatus(walkthroughId),
    ]).then(([def, sts]) => {
      if (def) setDefinition(def);
      if (sts) setStatuses(Array.isArray(sts) ? sts : sts.steps || []);
      setLoading(false);
    });
  };

  useEffect(() => { load(); }, [walkthroughId]);

  const statusMap = {};
  statuses.forEach(s => { statusMap[s.stepId] = s; });

  return (
    <div style={{ position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.4)', zIndex: 1000 }} onClick={onClose}>
      <div
        onClick={e => e.stopPropagation()}
        style={{
          position: 'fixed', top: 0, right: 0, bottom: 0, width: 520,
          background: '#fff', boxShadow: '-4px 0 24px rgba(0,0,0,0.12)',
          overflowY: 'auto', zIndex: 1001, display: 'flex', flexDirection: 'column',
        }}
      >
        {/* Header */}
        <div style={{
          display: 'flex', alignItems: 'center', padding: '16px 20px',
          borderBottom: '1px solid #e0e0e0', flexShrink: 0,
        }}>
          <div style={{ fontFamily: DISPLAY, fontSize: 18, fontWeight: 700, flex: 1 }}>
            {definition?.title || 'Loading...'}
          </div>
          <button
            onClick={onClose}
            style={{
              background: 'none', border: 'none', fontSize: 22, cursor: 'pointer',
              color: '#6a6e73', padding: '4px 8px', lineHeight: 1,
            }}
          >
            &times;
          </button>
        </div>
        {/* Body */}
        <div style={{ flex: 1, padding: '20px 20px 32px' }}>
          {loading && <div style={{ color: '#6a6e73', fontSize: 13 }}>Loading walkthrough...</div>}
          {!loading && definition?.steps?.map((step, i) => (
            <StepItem
              key={step.id}
              step={step}
              stepIndex={i}
              stepStatus={statusMap[step.id]}
              walkthroughId={walkthroughId}
              formValues={formValues}
              setFormValues={setFormValues}
              onExecuted={load}
            />
          ))}
        </div>
      </div>
    </div>
  );
}

// --- Connector card ---

function ConnectorCard({ wt, statusInfo, onClick }) {
  return (
    <Card style={{ cursor: 'pointer', transition: 'box-shadow 0.15s' }} onClick={onClick}>
      <div style={{ display: 'flex', alignItems: 'flex-start', gap: 12 }}>
        <div style={{
          width: 40, height: 40, borderRadius: 8, background: PAL.blue.bg,
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          fontSize: 18, fontWeight: 700, color: PAL.blue.fg, flexShrink: 0,
        }}>
          {wt.title?.charAt(0) || '?'}
        </div>
        <div style={{ flex: 1 }}>
          <div style={{ fontFamily: DISPLAY, fontSize: 15, fontWeight: 600, marginBottom: 4 }}>{wt.title}</div>
          <div style={{ fontSize: 12, color: '#6a6e73', marginBottom: 8 }}>{wt.stepCount} steps</div>
          <Badge c={statusInfo.color}>{statusInfo.label}</Badge>
        </div>
        <div style={{ color: '#8a8d90', fontSize: 18 }}>&rsaquo;</div>
      </div>
    </Card>
  );
}

// --- Main screen ---

export function Integrations() {
  const [walkthroughs, setWalkthroughs] = useState([]);
  const [statusMap, setStatusMap] = useState({});
  const [activeWalkthrough, setActiveWalkthrough] = useState(null);
  const [loading, setLoading] = useState(true);

  const loadAll = async () => {
    const list = await fetchWalkthroughs();
    if (!list) { setLoading(false); return; }
    const items = Array.isArray(list) ? list : list.walkthroughs || [];
    setWalkthroughs(items);
    const entries = await Promise.all(
      items.map(async wt => {
        const sts = await fetchWalkthroughStatus(wt.id);
        const arr = sts ? (Array.isArray(sts) ? sts : sts.steps || []) : [];
        return [wt.id, summarize(arr, wt.stepCount)];
      }),
    );
    setStatusMap(Object.fromEntries(entries));
    setLoading(false);
  };

  useEffect(() => { loadAll(); }, []);

  const handleClose = () => {
    setActiveWalkthrough(null);
    loadAll();
  };

  return (
    <div data-screen-label="Integrations">
      <PageH title="Integrations" sub="Connect payment processors and billing systems" />
      {loading && <div style={{ color: '#6a6e73', fontSize: 13 }}>Loading integrations...</div>}
      {!loading && walkthroughs.length === 0 && (
        <Card><div style={{ color: '#6a6e73', fontSize: 13, padding: 8 }}>No integrations available. Deploy a walkthrough definition to get started.</div></Card>
      )}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(300px, 1fr))', gap: 14 }}>
        {walkthroughs.map(wt => (
          <ConnectorCard
            key={wt.id}
            wt={wt}
            statusInfo={statusMap[wt.id] || { label: 'Not started', color: 'grey' }}
            onClick={() => setActiveWalkthrough(wt.id)}
          />
        ))}
      </div>
      {activeWalkthrough && <WalkthroughWizard walkthroughId={activeWalkthrough} onClose={handleClose} />}
    </div>
  );
}

export default Integrations;
