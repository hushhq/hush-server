import { useState, useEffect, useCallback } from 'react';
import { listGuilds, setGuildMemberCap, getConfig } from '../lib/adminApi.js';

/**
 * GuildListPage - shows guild infrastructure metrics.
 * Displays UUIDs and counts only. No guild names (blind relay boundary).
 */

const PAGE_STYLES = {
  container: {
    padding: '24px 28px',
    overflowY: 'auto',
    height: '100%',
  },
  header: {
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'space-between',
    marginBottom: '20px',
  },
  title: {
    fontSize: '1.1rem',
    fontWeight: 600,
    color: 'var(--text)',
  },
  count: {
    fontSize: '0.8rem',
    color: 'var(--text-muted)',
    marginTop: '2px',
  },
  table: {
    width: '100%',
    borderCollapse: 'collapse',
    fontSize: '0.82rem',
  },
  th: {
    textAlign: 'left',
    padding: '8px 12px',
    color: 'var(--text-muted)',
    fontWeight: 600,
    fontSize: '0.72rem',
    textTransform: 'uppercase',
    letterSpacing: '0.06em',
    borderBottom: '1px solid var(--border)',
    background: 'var(--surface)',
    cursor: 'pointer',
    userSelect: 'none',
    whiteSpace: 'nowrap',
  },
  td: {
    padding: '10px 12px',
    borderBottom: '1px solid var(--border)',
    color: 'var(--text-secondary)',
    verticalAlign: 'middle',
  },
  tdMono: {
    padding: '10px 12px',
    borderBottom: '1px solid var(--border)',
    color: 'var(--text-secondary)',
    fontFamily: 'var(--font-mono)',
    fontSize: '0.78rem',
    verticalAlign: 'middle',
  },
  copyBtn: {
    background: 'none',
    border: 'none',
    color: 'var(--text-muted)',
    cursor: 'pointer',
    padding: '2px 4px',
    fontSize: '0.75rem',
    transition: 'color 0.12s ease',
    marginLeft: '4px',
  },
  capCell: {
    display: 'flex',
    alignItems: 'center',
    gap: '6px',
    flexWrap: 'nowrap',
  },
  capInput: {
    width: '60px',
    padding: '3px 6px',
    background: 'var(--bg)',
    border: '1px solid var(--border)',
    borderRadius: 'var(--radius-sm)',
    color: 'var(--text)',
    fontSize: '0.78rem',
    outline: 'none',
  },
  capBtn: {
    background: 'none',
    border: '1px solid var(--border)',
    borderRadius: 'var(--radius-sm)',
    color: 'var(--text-muted)',
    cursor: 'pointer',
    padding: '2px 8px',
    fontSize: '0.72rem',
    fontWeight: 500,
    transition: 'color 0.12s ease, border-color 0.12s ease',
    whiteSpace: 'nowrap',
  },
  capLabel: {
    fontSize: '0.78rem',
    color: 'var(--text-muted)',
    whiteSpace: 'nowrap',
  },
  capEffective: {
    fontSize: '0.72rem',
    color: 'var(--text-muted)',
    fontStyle: 'italic',
  },
  badge: (color) => ({
    display: 'inline-block',
    padding: '2px 8px',
    borderRadius: '3px',
    fontSize: '0.72rem',
    fontWeight: 500,
    background: color === 'green' ? 'rgba(34, 197, 94, 0.12)' : 'rgba(255,255,255,0.06)',
    color: color === 'green' ? 'var(--success)' : 'var(--text-muted)',
  }),
  empty: {
    textAlign: 'center',
    padding: '60px 0',
    color: 'var(--text-muted)',
    fontSize: '0.9rem',
  },
  error: {
    padding: '16px',
    borderRadius: 'var(--radius-md)',
    background: 'rgba(239, 68, 68, 0.08)',
    border: '1px solid rgba(239, 68, 68, 0.2)',
    color: 'var(--danger)',
    fontSize: '0.85rem',
    marginBottom: '16px',
  },
};

const COLUMNS = [
  { key: 'id', label: 'Guild ID' },
  { key: 'memberCount', label: 'Members', numeric: true },
  { key: 'memberCap', label: 'Member Cap', sortable: false },
  { key: 'messageCount', label: 'Messages', numeric: true },
  { key: 'activeMembers30d', label: 'Active (30d)', numeric: true },
  { key: 'storageBytes', label: 'Storage', numeric: true },
  { key: 'accessPolicy', label: 'Access' },
  { key: 'discoverable', label: 'Discoverable' },
  { key: 'createdAt', label: 'Created' },
];

function formatBytes(bytes) {
  if (bytes == null) return '-';
  if (bytes === 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB'];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return `${(bytes / 1024 ** i).toFixed(1)} ${units[i]}`;
}

function formatDate(iso) {
  if (!iso) return '-';
  return new Date(iso).toLocaleDateString('en-US', { year: 'numeric', month: 'short', day: 'numeric' });
}

function SortIndicator({ active, dir }) {
  if (!active) return <span style={{ marginLeft: '4px', opacity: 0.3 }}>⇅</span>;
  return <span style={{ marginLeft: '4px' }}>{dir === 'asc' ? '↑' : '↓'}</span>;
}

function CopyButton({ text }) {
  const [copied, setCopied] = useState(false);

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch { /* ignore */ }
  };

  return (
    <button type="button" style={PAGE_STYLES.copyBtn} onClick={handleCopy} title="Copy full UUID">
      {copied ? '✓' : '⧉'}
    </button>
  );
}

/**
 * Inline member cap override control for a single guild row.
 * Shows current override (or "Default"), input to set a new value, and clear button.
 */
function MemberCapCell({ guild, instanceDefault, onUpdated }) {
  const [inputValue, setInputValue] = useState('');
  const [saving, setSaving] = useState(false);
  const [feedback, setFeedback] = useState('');

  const hasOverride = guild.memberCapOverride != null && guild.memberCapOverride > 0;

  const handleSet = async () => {
    const parsed = parseInt(inputValue, 10);
    if (isNaN(parsed) || parsed < 1) {
      setFeedback('Min 1');
      setTimeout(() => setFeedback(''), 2000);
      return;
    }
    setSaving(true);
    setFeedback('');
    try {
      const result = await setGuildMemberCap(guild.id, parsed);
      onUpdated(guild.id, result);
      setInputValue('');
      setFeedback(`Cap: ${result.effectiveCap}`);
      setTimeout(() => setFeedback(''), 3000);
    } catch (e) {
      setFeedback(e.message);
      setTimeout(() => setFeedback(''), 4000);
    }
    setSaving(false);
  };

  const handleClear = async () => {
    setSaving(true);
    setFeedback('');
    try {
      const result = await setGuildMemberCap(guild.id, 0);
      onUpdated(guild.id, result);
      setInputValue('');
      setFeedback(`Cleared (cap: ${result.effectiveCap})`);
      setTimeout(() => setFeedback(''), 3000);
    } catch (e) {
      setFeedback(e.message);
      setTimeout(() => setFeedback(''), 4000);
    }
    setSaving(false);
  };

  const currentLabel = hasOverride
    ? String(guild.memberCapOverride)
    : `Default${instanceDefault != null ? ` (${instanceDefault})` : ''}`;

  return (
    <div>
      <div style={PAGE_STYLES.capCell}>
        <span style={PAGE_STYLES.capLabel}>{currentLabel}</span>
        <input
          type="number"
          min="1"
          style={PAGE_STYLES.capInput}
          value={inputValue}
          onChange={(e) => setInputValue(e.target.value)}
          onKeyDown={(e) => { if (e.key === 'Enter') handleSet(); }}
          placeholder="#"
          disabled={saving}
        />
        <button
          type="button"
          style={PAGE_STYLES.capBtn}
          onClick={handleSet}
          disabled={saving || !inputValue}
        >
          Set
        </button>
        {hasOverride && (
          <button
            type="button"
            style={{ ...PAGE_STYLES.capBtn, color: 'var(--danger)' }}
            onClick={handleClear}
            disabled={saving}
          >
            Clear
          </button>
        )}
      </div>
      {feedback && <div style={PAGE_STYLES.capEffective}>{feedback}</div>}
    </div>
  );
}

export default function GuildListPage() {
  const [guilds, setGuilds] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [sortKey, setSortKey] = useState('createdAt');
  const [sortDir, setSortDir] = useState('desc');
  const [instanceDefaultCap, setInstanceDefaultCap] = useState(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const [data, config] = await Promise.all([listGuilds(), getConfig()]);
      setGuilds(Array.isArray(data) ? data : []);
      if (config.maxMembersPerServer != null) {
        setInstanceDefaultCap(config.maxMembersPerServer);
      }
    } catch (e) {
      setError(e.message);
    }
    setLoading(false);
  }, []);

  useEffect(() => { load(); }, [load]);

  const handleCapUpdated = useCallback((guildId, result) => {
    setGuilds((prev) =>
      prev.map((g) =>
        g.id === guildId
          ? { ...g, memberCapOverride: result.memberCapOverride ?? null }
          : g
      )
    );
  }, []);

  const handleSort = (key) => {
    if (sortKey === key) {
      setSortDir((d) => (d === 'asc' ? 'desc' : 'asc'));
    } else {
      setSortKey(key);
      setSortDir('desc');
    }
  };

  const sorted = [...guilds].sort((a, b) => {
    let av = a[sortKey];
    let bv = b[sortKey];
    if (sortKey === 'createdAt') {
      av = av ? new Date(av).getTime() : 0;
      bv = bv ? new Date(bv).getTime() : 0;
    }
    if (av < bv) return sortDir === 'asc' ? -1 : 1;
    if (av > bv) return sortDir === 'asc' ? 1 : -1;
    return 0;
  });

  return (
    <div style={PAGE_STYLES.container}>
      <div style={PAGE_STYLES.header}>
        <div>
          <div style={PAGE_STYLES.title}>Guilds</div>
          {!loading && (
            <div style={PAGE_STYLES.count}>{guilds.length} guild{guilds.length !== 1 ? 's' : ''}</div>
          )}
        </div>
        <button type="button" className="btn btn-secondary" onClick={load} disabled={loading}>
          {loading ? 'Loading...' : 'Refresh'}
        </button>
      </div>

      {error && <div style={PAGE_STYLES.error}>{error}</div>}

      {!loading && guilds.length === 0 && !error && (
        <div style={PAGE_STYLES.empty}>No guilds found on this instance.</div>
      )}

      {(guilds.length > 0 || loading) && (
        <table style={PAGE_STYLES.table}>
          <thead>
            <tr>
              {COLUMNS.map((col) => (
                <th
                  key={col.key}
                  style={{
                    ...PAGE_STYLES.th,
                    ...(col.sortable === false ? { cursor: 'default' } : {}),
                  }}
                  onClick={col.sortable !== false ? () => handleSort(col.key) : undefined}
                >
                  {col.label}
                  {col.sortable !== false && (
                    <SortIndicator active={sortKey === col.key} dir={sortDir} />
                  )}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {sorted.map((g) => (
              <tr key={g.id}>
                <td style={PAGE_STYLES.tdMono}>
                  {g.id ? `${g.id.slice(0, 8)}...` : '-'}
                  {g.id && <CopyButton text={g.id} />}
                </td>
                <td style={PAGE_STYLES.td}>{g.memberCount ?? '-'}</td>
                <td style={PAGE_STYLES.td}>
                  <MemberCapCell
                    guild={g}
                    instanceDefault={instanceDefaultCap}
                    onUpdated={handleCapUpdated}
                  />
                </td>
                <td style={PAGE_STYLES.td}>{g.messageCount ?? '-'}</td>
                <td style={PAGE_STYLES.td}>{g.activeMembers30d ?? '-'}</td>
                <td style={PAGE_STYLES.td}>{formatBytes(g.storageBytes)}</td>
                <td style={PAGE_STYLES.td}>
                  {g.accessPolicy ? (
                    <span style={PAGE_STYLES.badge(g.accessPolicy === 'public' ? 'green' : 'neutral')}>
                      {g.accessPolicy}
                    </span>
                  ) : '-'}
                </td>
                <td style={PAGE_STYLES.td}>
                  {g.discoverable != null ? (
                    <span style={PAGE_STYLES.badge(g.discoverable ? 'green' : 'neutral')}>
                      {g.discoverable ? 'yes' : 'no'}
                    </span>
                  ) : '-'}
                </td>
                <td style={PAGE_STYLES.td}>{formatDate(g.createdAt)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
