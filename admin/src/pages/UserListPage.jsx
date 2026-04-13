import { useState, useEffect, useCallback } from 'react';
import { listUsers, instanceBan, instanceUnban } from '../lib/adminApi.js';

/**
 * UserListPage - shows user UUIDs, roles, creation dates, and status.
 * No usernames or identities are displayed (blind relay boundary).
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
    marginBottom: '16px',
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
  searchBox: {
    marginBottom: '16px',
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
    marginLeft: '4px',
  },
  badge: (variant) => {
    const variants = {
      admin: { bg: 'rgba(213, 79, 18, 0.15)', color: 'var(--accent)' },
      banned: { bg: 'rgba(239, 68, 68, 0.12)', color: 'var(--danger)' },
      member: { bg: 'rgba(255, 255, 255, 0.06)', color: 'var(--text-muted)' },
    };
    const v = variants[variant] || variants.member;
    return {
      display: 'inline-block',
      padding: '2px 8px',
      borderRadius: '3px',
      fontSize: '0.72rem',
      fontWeight: 500,
      background: v.bg,
      color: v.color,
    };
  },
  actionCell: {
    padding: '10px 12px',
    borderBottom: '1px solid var(--border)',
    verticalAlign: 'middle',
    display: 'flex',
    gap: '6px',
    alignItems: 'center',
  },
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
  modal: {
    position: 'fixed',
    inset: 0,
    background: 'rgba(0,0,0,0.7)',
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    zIndex: 100,
  },
  modalBox: {
    background: 'var(--surface)',
    border: '1px solid var(--border)',
    borderRadius: 'var(--radius-md)',
    padding: '24px',
    width: '400px',
    display: 'flex',
    flexDirection: 'column',
    gap: '12px',
  },
  modalTitle: {
    fontSize: '0.95rem',
    fontWeight: 600,
    color: 'var(--text)',
  },
  modalNote: {
    fontSize: '0.8rem',
    color: 'var(--text-muted)',
    fontFamily: 'var(--font-mono)',
    wordBreak: 'break-all',
  },
};

function formatDate(iso) {
  if (!iso) return '-';
  return new Date(iso).toLocaleDateString('en-US', { year: 'numeric', month: 'short', day: 'numeric' });
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

function BanModal({ user, onConfirm, onCancel }) {
  const [reason, setReason] = useState('');
  const [duration, setDuration] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  const handleConfirm = async () => {
    if (!reason.trim()) return;
    setLoading(true);
    setError('');
    try {
      const expiresAt = duration ? Math.floor(Date.now() / 1000) + parseInt(duration, 10) : null;
      await onConfirm(user.id, reason.trim(), expiresAt);
    } catch (e) {
      setError(e.message);
    }
    setLoading(false);
  };

  return (
    <div style={PAGE_STYLES.modal} onClick={(e) => { if (e.target === e.currentTarget) onCancel(); }}>
      <div style={PAGE_STYLES.modalBox}>
        <div style={PAGE_STYLES.modalTitle}>Instance Ban</div>
        <div style={PAGE_STYLES.modalNote}>User: {user.id}</div>
        <div>
          <label style={{ fontSize: '0.75rem', color: 'var(--text-muted)', display: 'block', marginBottom: '4px' }}>
            Reason (required)
          </label>
          <textarea
            className="input"
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            placeholder="Reason for the instance ban..."
            style={{ minHeight: '72px', resize: 'vertical' }}
            autoFocus
          />
        </div>
        <div>
          <label style={{ fontSize: '0.75rem', color: 'var(--text-muted)', display: 'block', marginBottom: '4px' }}>
            Duration
          </label>
          <select className="select" value={duration} onChange={(e) => setDuration(e.target.value)}>
            <option value="">Permanent</option>
            <option value="3600">1 hour</option>
            <option value="86400">24 hours</option>
            <option value="604800">7 days</option>
            <option value="2592000">30 days</option>
          </select>
        </div>
        {error && <div style={{ fontSize: '0.8rem', color: 'var(--danger)' }}>{error}</div>}
        <div style={{ display: 'flex', gap: '8px', justifyContent: 'flex-end' }}>
          <button type="button" className="btn btn-secondary" onClick={onCancel} disabled={loading}>Cancel</button>
          <button
            type="button"
            className="btn btn-danger"
            onClick={handleConfirm}
            disabled={!reason.trim() || loading}
          >
            {loading ? 'Banning...' : 'Confirm Ban'}
          </button>
        </div>
      </div>
    </div>
  );
}

function UnbanModal({ user, onConfirm, onCancel }) {
  const [reason, setReason] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  const handleConfirm = async () => {
    if (!reason.trim()) return;
    setLoading(true);
    setError('');
    try {
      await onConfirm(user.id, reason.trim());
    } catch (e) {
      setError(e.message);
    }
    setLoading(false);
  };

  return (
    <div style={PAGE_STYLES.modal} onClick={(e) => { if (e.target === e.currentTarget) onCancel(); }}>
      <div style={PAGE_STYLES.modalBox}>
        <div style={PAGE_STYLES.modalTitle}>Lift Instance Ban</div>
        <div style={PAGE_STYLES.modalNote}>User: {user.id}</div>
        <div>
          <label style={{ fontSize: '0.75rem', color: 'var(--text-muted)', display: 'block', marginBottom: '4px' }}>
            Reason (required)
          </label>
          <input
            type="text"
            className="input"
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            placeholder="Reason for lifting the ban..."
            autoFocus
          />
        </div>
        {error && <div style={{ fontSize: '0.8rem', color: 'var(--danger)' }}>{error}</div>}
        <div style={{ display: 'flex', gap: '8px', justifyContent: 'flex-end' }}>
          <button type="button" className="btn btn-secondary" onClick={onCancel} disabled={loading}>Cancel</button>
          <button
            type="button"
            className="btn btn-primary"
            onClick={handleConfirm}
            disabled={!reason.trim() || loading}
          >
            {loading ? 'Unbanning...' : 'Confirm Unban'}
          </button>
        </div>
      </div>
    </div>
  );
}

export default function UserListPage() {
  const [users, setUsers] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [search, setSearch] = useState('');
  const [banTarget, setBanTarget] = useState(null);
  const [unbanTarget, setUnbanTarget] = useState(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const data = await listUsers();
      setUsers(Array.isArray(data) ? data : []);
    } catch (e) {
      setError(e.message);
    }
    setLoading(false);
  }, []);

  useEffect(() => { load(); }, [load]);

  const handleBan = useCallback(async (userId, reason, expiresAt) => {
    await instanceBan(userId, reason, expiresAt);
    setBanTarget(null);
    await load();
  }, [load]);

  const handleUnban = useCallback(async (userId, reason) => {
    await instanceUnban(userId, reason);
    setUnbanTarget(null);
    await load();
  }, [load]);

  const filtered = search.trim()
    ? users.filter((u) => u.id?.toLowerCase().includes(search.trim().toLowerCase()))
    : users;

  return (
    <div style={PAGE_STYLES.container}>
      {banTarget && (
        <BanModal
          user={banTarget}
          onConfirm={handleBan}
          onCancel={() => setBanTarget(null)}
        />
      )}
      {unbanTarget && (
        <UnbanModal
          user={unbanTarget}
          onConfirm={handleUnban}
          onCancel={() => setUnbanTarget(null)}
        />
      )}

      <div style={PAGE_STYLES.header}>
        <div>
          <div style={PAGE_STYLES.title}>Users</div>
          {!loading && (
            <div style={PAGE_STYLES.count}>{users.length} user{users.length !== 1 ? 's' : ''}</div>
          )}
        </div>
        <button type="button" className="btn btn-secondary" onClick={load} disabled={loading}>
          {loading ? 'Loading...' : 'Refresh'}
        </button>
      </div>

      <div style={PAGE_STYLES.searchBox}>
        <input
          type="text"
          className="input"
          placeholder="Search by UUID..."
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          style={{ maxWidth: '360px' }}
        />
      </div>

      {error && <div style={PAGE_STYLES.error}>{error}</div>}

      {!loading && filtered.length === 0 && !error && (
        <div style={PAGE_STYLES.empty}>
          {search.trim() ? 'No users match that UUID.' : 'No users found.'}
        </div>
      )}

      {filtered.length > 0 && (
        <table style={PAGE_STYLES.table}>
          <thead>
            <tr>
              <th style={PAGE_STYLES.th}>User ID</th>
              <th style={PAGE_STYLES.th}>Instance Role</th>
              <th style={PAGE_STYLES.th}>Created</th>
              <th style={PAGE_STYLES.th}>Status</th>
              <th style={PAGE_STYLES.th}>Actions</th>
            </tr>
          </thead>
          <tbody>
            {filtered.map((u) => {
              const isBanned = u.status === 'banned';
              return (
                <tr key={u.id}>
                  <td style={PAGE_STYLES.tdMono}>
                    {u.id ? `${u.id.slice(0, 8)}...` : '-'}
                    {u.id && <CopyButton text={u.id} />}
                  </td>
                  <td style={PAGE_STYLES.td}>
                    <span style={PAGE_STYLES.badge(u.role === 'admin' ? 'admin' : 'member')}>
                      {u.role || 'member'}
                    </span>
                  </td>
                  <td style={PAGE_STYLES.td}>{formatDate(u.createdAt)}</td>
                  <td style={PAGE_STYLES.td}>
                    {isBanned ? (
                      <span style={PAGE_STYLES.badge('banned')}>banned</span>
                    ) : (
                      <span style={PAGE_STYLES.badge('member')}>active</span>
                    )}
                  </td>
                  <td style={{ ...PAGE_STYLES.td, whiteSpace: 'nowrap' }}>
                    {isBanned ? (
                      <button
                        type="button"
                        className="btn btn-secondary"
                        style={{ padding: '4px 10px', fontSize: '0.78rem' }}
                        onClick={() => setUnbanTarget(u)}
                      >
                        Unban
                      </button>
                    ) : (
                      <button
                        type="button"
                        className="btn btn-danger"
                        style={{ padding: '4px 10px', fontSize: '0.78rem' }}
                        onClick={() => setBanTarget(u)}
                      >
                        Ban
                      </button>
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
    </div>
  );
}
