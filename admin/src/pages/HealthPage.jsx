import { useState, useEffect, useCallback, useRef } from 'react';
import { getHealth } from '../lib/adminApi.js';

/**
 * HealthPage - DB status, uptime, and version info.
 * Auto-refreshes every 30 seconds.
 */

const REFRESH_INTERVAL_MS = 30_000;

const PAGE_STYLES = {
  container: {
    padding: '24px 28px',
    overflowY: 'auto',
    height: '100%',
    maxWidth: '640px',
  },
  header: {
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'space-between',
    marginBottom: '24px',
  },
  title: {
    fontSize: '1.1rem',
    fontWeight: 600,
    color: 'var(--text)',
  },
  countdown: {
    fontSize: '0.75rem',
    color: 'var(--text-muted)',
  },
  card: {
    background: 'var(--surface)',
    border: '1px solid var(--border)',
    borderRadius: 'var(--radius-md)',
    padding: '20px 24px',
    marginBottom: '12px',
  },
  cardTitle: {
    fontSize: '0.72rem',
    fontWeight: 700,
    color: 'var(--text-muted)',
    textTransform: 'uppercase',
    letterSpacing: '0.08em',
    marginBottom: '12px',
  },
  row: {
    display: 'flex',
    alignItems: 'baseline',
    gap: '12px',
    padding: '6px 0',
    borderBottom: '1px solid var(--border)',
  },
  rowLast: {
    display: 'flex',
    alignItems: 'baseline',
    gap: '12px',
    padding: '6px 0',
  },
  rowLabel: {
    fontSize: '0.82rem',
    color: 'var(--text-muted)',
    width: '140px',
    flexShrink: 0,
  },
  rowValue: {
    fontSize: '0.85rem',
    color: 'var(--text)',
    fontFamily: 'var(--font-mono)',
  },
  status: (ok) => ({
    display: 'inline-flex',
    alignItems: 'center',
    gap: '6px',
    fontSize: '0.85rem',
    color: ok ? 'var(--success)' : 'var(--danger)',
    fontWeight: 500,
  }),
  dot: (ok) => ({
    width: '8px',
    height: '8px',
    borderRadius: '50%',
    background: ok ? 'var(--success)' : 'var(--danger)',
    flexShrink: 0,
  }),
  error: {
    padding: '16px',
    borderRadius: 'var(--radius-md)',
    background: 'rgba(239, 68, 68, 0.08)',
    border: '1px solid rgba(239, 68, 68, 0.2)',
    color: 'var(--danger)',
    fontSize: '0.85rem',
  },
  skeleton: {
    height: '12px',
    background: 'var(--elevated)',
    borderRadius: '3px',
    width: '120px',
    display: 'inline-block',
  },
};

function formatUptime(seconds) {
  if (seconds == null) return '-';
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = Math.floor(seconds % 60);
  if (d > 0) return `${d}d ${h}h ${m}m`;
  if (h > 0) return `${h}h ${m}m ${s}s`;
  if (m > 0) return `${m}m ${s}s`;
  return `${s}s`;
}

function formatTimestamp(iso) {
  if (!iso) return '-';
  return new Date(iso).toLocaleString();
}

export default function HealthPage() {
  const [health, setHealth] = useState(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [lastRefresh, setLastRefresh] = useState(null);
  const [countdown, setCountdown] = useState(REFRESH_INTERVAL_MS / 1000);
  const timerRef = useRef(null);
  const countdownRef = useRef(null);

  const cancelledRef = useRef(false);

  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const data = await getHealth();
      if (cancelledRef.current) return;
      setHealth(data);
      setLastRefresh(new Date());
    } catch (e) {
      if (cancelledRef.current) return;
      setError(e.message);
    }
    if (!cancelledRef.current) {
      setLoading(false);
      setCountdown(REFRESH_INTERVAL_MS / 1000);
    }
  }, []);

  // Initial load and auto-refresh every 30s
  useEffect(() => {
    cancelledRef.current = false;
    load();
    timerRef.current = setInterval(load, REFRESH_INTERVAL_MS);
    return () => {
      cancelledRef.current = true;
      clearInterval(timerRef.current);
    };
  }, [load]);

  // Countdown ticker
  useEffect(() => {
    countdownRef.current = setInterval(() => {
      setCountdown((c) => Math.max(0, c - 1));
    }, 1000);
    return () => clearInterval(countdownRef.current);
  }, []);

  const dbOk = health?.dbStatus === 'ok';

  return (
    <div style={PAGE_STYLES.container}>
      <div style={PAGE_STYLES.header}>
        <div style={PAGE_STYLES.title}>Health</div>
        <div style={{ display: 'flex', alignItems: 'center', gap: '12px' }}>
          <span style={PAGE_STYLES.countdown}>
            {loading ? 'Refreshing...' : `Refreshes in ${countdown}s`}
          </span>
          <button type="button" className="btn btn-secondary" onClick={load} disabled={loading}>
            Refresh now
          </button>
        </div>
      </div>

      {error && !health && (
        <div style={PAGE_STYLES.error}>{error}</div>
      )}

      <div style={PAGE_STYLES.card}>
        <div style={PAGE_STYLES.cardTitle}>Database</div>
        <div style={PAGE_STYLES.row}>
          <span style={PAGE_STYLES.rowLabel}>Status</span>
          <span>
            {loading && !health ? (
              <span style={PAGE_STYLES.skeleton} />
            ) : (
              <span style={PAGE_STYLES.status(dbOk)}>
                <span style={PAGE_STYLES.dot(dbOk)} />
                {health?.dbStatus || (error ? 'unreachable' : '-')}
              </span>
            )}
          </span>
        </div>
        <div style={PAGE_STYLES.rowLast}>
          <span style={PAGE_STYLES.rowLabel}>Last check</span>
          <span style={PAGE_STYLES.rowValue}>
            {lastRefresh ? lastRefresh.toLocaleTimeString() : '-'}
          </span>
        </div>
      </div>

      <div style={PAGE_STYLES.card}>
        <div style={PAGE_STYLES.cardTitle}>Instance</div>
        <div style={PAGE_STYLES.row}>
          <span style={PAGE_STYLES.rowLabel}>Version</span>
          <span style={PAGE_STYLES.rowValue}>
            {loading && !health ? <span style={PAGE_STYLES.skeleton} /> : (health?.version || '-')}
          </span>
        </div>
        <div style={PAGE_STYLES.row}>
          <span style={PAGE_STYLES.rowLabel}>Uptime</span>
          <span style={PAGE_STYLES.rowValue}>
            {loading && !health ? (
              <span style={PAGE_STYLES.skeleton} />
            ) : (
              formatUptime(health?.uptimeSeconds)
            )}
          </span>
        </div>
        <div style={PAGE_STYLES.rowLast}>
          <span style={PAGE_STYLES.rowLabel}>Started at</span>
          <span style={PAGE_STYLES.rowValue}>
            {loading && !health ? (
              <span style={PAGE_STYLES.skeleton} />
            ) : (
              formatTimestamp(health?.startedAt)
            )}
          </span>
        </div>
      </div>

      {health && Object.keys(health).length > 0 && (
        <div style={PAGE_STYLES.card}>
          <div style={PAGE_STYLES.cardTitle}>Raw Response</div>
          <pre style={{
            fontSize: '0.75rem',
            color: 'var(--text-muted)',
            fontFamily: 'var(--font-mono)',
            overflowX: 'auto',
            lineHeight: 1.6,
            margin: 0,
          }}>
            {JSON.stringify(health, null, 2)}
          </pre>
        </div>
      )}
    </div>
  );
}
