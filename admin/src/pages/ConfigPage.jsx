import { useState, useEffect, useCallback } from 'react';
import {
  getConfig,
  updateConfig,
  listTemplates,
  createTemplate,
  updateTemplate,
  deleteTemplate,
} from '../lib/adminApi.js';

/**
 * ConfigPage - instance config management and server template CRUD.
 */

const PAGE_STYLES = {
  container: {
    padding: '24px 28px',
    overflowY: 'auto',
    height: '100%',
    maxWidth: '720px',
  },
  section: {
    marginBottom: '40px',
  },
  sectionTitle: {
    fontSize: '1rem',
    fontWeight: 600,
    color: 'var(--text)',
    marginBottom: '4px',
    paddingBottom: '12px',
    borderBottom: '1px solid var(--border)',
  },
  fieldRow: {
    marginBottom: '20px',
  },
  label: {
    display: 'block',
    fontSize: '0.75rem',
    fontWeight: 600,
    color: 'var(--text-muted)',
    textTransform: 'uppercase',
    letterSpacing: '0.06em',
    marginBottom: '6px',
  },
  note: {
    fontSize: '0.75rem',
    color: 'var(--text-muted)',
    marginTop: '4px',
  },
  btnRow: {
    display: 'flex',
    gap: '10px',
    alignItems: 'center',
    marginTop: '4px',
  },
  success: {
    fontSize: '0.82rem',
    color: 'var(--success)',
  },
  error: {
    fontSize: '0.82rem',
    color: 'var(--danger)',
  },
  templateCard: {
    display: 'flex',
    alignItems: 'center',
    gap: '12px',
    padding: '12px 14px',
    background: 'var(--surface)',
    border: '1px solid var(--border)',
    borderRadius: 'var(--radius-md)',
    marginBottom: '8px',
  },
  templateName: {
    flex: 1,
    minWidth: 0,
    fontSize: '0.9rem',
    fontWeight: 500,
    color: 'var(--text)',
  },
  templateMeta: {
    fontSize: '0.75rem',
    color: 'var(--text-muted)',
    marginTop: '2px',
  },
  defaultBadge: {
    display: 'inline-block',
    padding: '1px 6px',
    borderRadius: '3px',
    fontSize: '0.68rem',
    fontWeight: 600,
    background: 'rgba(34, 197, 94, 0.15)',
    color: 'var(--success)',
    marginLeft: '6px',
    letterSpacing: '0.04em',
    textTransform: 'uppercase',
  },
  editBox: {
    background: 'var(--surface)',
    border: '1px solid var(--border)',
    borderRadius: 'var(--radius-md)',
    padding: '20px',
    marginBottom: '16px',
  },
  channelRow: {
    display: 'flex',
    alignItems: 'center',
    gap: '8px',
    padding: '7px 10px',
    background: 'var(--elevated)',
    borderRadius: 'var(--radius-sm)',
    marginBottom: '4px',
  },
  channelType: {
    fontSize: '0.75rem',
    color: 'var(--text-muted)',
    width: '48px',
    flexShrink: 0,
  },
  channelName: {
    flex: 1,
    minWidth: 0,
    fontSize: '0.85rem',
    color: 'var(--text-secondary)',
  },
  smallBtn: {
    background: 'none',
    border: 'none',
    color: 'var(--text-muted)',
    cursor: 'pointer',
    padding: '2px 6px',
    fontSize: '0.9rem',
    transition: 'color 0.12s ease',
  },
};

const CHANNEL_TYPE_ICONS = {
  system: '🛡',
  text: '#',
  voice: '🔊',
  category: '📁',
};

const DEFAULT_TEMPLATE_CHANNELS = [
  { name: 'system', type: 'system', position: -1 },
  { name: 'general', type: 'text', position: 0 },
  { name: 'General', type: 'voice', voiceMode: 'quality', position: 1 },
];

// ─── Instance Config Section ──────────────────────────────

function InstanceConfigSection() {
  const [config, setConfig] = useState(null);
  const [regMode, setRegMode] = useState('open');
  const [guildDiscovery, setGuildDiscovery] = useState('disabled');
  const [serverCreationPolicy, setServerCreationPolicy] = useState('open');
  const [maxServersPerUser, setMaxServersPerUser] = useState('');
  const [maxMembersPerServer, setMaxMembersPerServer] = useState('');
  const [name, setName] = useState('');
  const [iconUrl, setIconUrl] = useState('');
  const [saving, setSaving] = useState(false);
  const [success, setSuccess] = useState('');
  const [error, setError] = useState('');

  useEffect(() => {
    let cancelled = false;
    getConfig().then((data) => {
      if (cancelled) return;
      setConfig(data);
      setRegMode(data.registrationMode || 'open');
      setGuildDiscovery(data.guildDiscovery || 'disabled');
      setServerCreationPolicy(data.serverCreationPolicy || 'open');
      setMaxServersPerUser(data.maxServersPerUser != null ? String(data.maxServersPerUser) : '');
      setMaxMembersPerServer(data.maxMembersPerServer != null ? String(data.maxMembersPerServer) : '');
      setName(data.name || '');
      setIconUrl(data.iconUrl || '');
    }).catch((e) => {
      if (!cancelled) setError(e.message);
    });
    return () => { cancelled = true; };
  }, []);

  const handleSave = async () => {
    setSaving(true);
    setError('');
    setSuccess('');
    try {
      const updates = {
        name: name.trim() || undefined,
        iconUrl: iconUrl.trim() || undefined,
        registrationMode: regMode,
        guildDiscovery,
        serverCreationPolicy,
      };
      const parsedMaxServers = parseInt(maxServersPerUser, 10);
      if (!isNaN(parsedMaxServers)) updates.maxServersPerUser = parsedMaxServers;
      const parsedMaxMembers = parseInt(maxMembersPerServer, 10);
      if (!isNaN(parsedMaxMembers)) updates.maxMembersPerServer = parsedMaxMembers;
      await updateConfig(updates);
      setSuccess('Configuration saved. Changes apply to clients on next connection.');
      setTimeout(() => setSuccess(''), 4000);
    } catch (e) {
      setError(e.message);
    }
    setSaving(false);
  };

  if (!config && !error) {
    return <div style={{ color: 'var(--text-muted)', fontSize: '0.85rem' }}>Loading configuration...</div>;
  }

  return (
    <div style={PAGE_STYLES.section}>
      <div style={PAGE_STYLES.sectionTitle}>Instance Configuration</div>

      <div style={PAGE_STYLES.fieldRow}>
        <label style={PAGE_STYLES.label}>Instance display name</label>
        <input
          type="text"
          className="input"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="e.g. My Hush Instance"
        />
        <div style={PAGE_STYLES.note}>
          Shown on the public handshake endpoint. Separate from guild names.
        </div>
      </div>

      <div style={PAGE_STYLES.fieldRow}>
        <label style={PAGE_STYLES.label}>Icon URL</label>
        <input
          type="url"
          className="input"
          value={iconUrl}
          onChange={(e) => setIconUrl(e.target.value)}
          placeholder="https://example.com/icon.png"
        />
      </div>

      <div style={PAGE_STYLES.fieldRow}>
        <label style={PAGE_STYLES.label}>Registration mode</label>
        <select
          className="select"
          value={regMode}
          onChange={(e) => setRegMode(e.target.value)}
        >
          <option value="open">Open</option>
          <option value="invite_only">Invite only</option>
          <option value="closed">Closed</option>
        </select>
        <div style={PAGE_STYLES.note}>
          Controls who can register new accounts on this instance.
        </div>
      </div>

      <div style={PAGE_STYLES.fieldRow}>
        <label style={PAGE_STYLES.label}>Server creation policy</label>
        <select
          className="select"
          value={serverCreationPolicy}
          onChange={(e) => setServerCreationPolicy(e.target.value)}
        >
          <option value="open">Open - any user can create guilds</option>
          <option value="paid">Paid - reserved for paid accounts</option>
          <option value="disabled">Disabled - only admins can create guilds</option>
        </select>
      </div>

      <div style={PAGE_STYLES.fieldRow}>
        <label style={PAGE_STYLES.label}>Guild discovery</label>
        <select
          className="select"
          value={guildDiscovery}
          onChange={(e) => setGuildDiscovery(e.target.value)}
        >
          <option value="disabled">Disabled</option>
          <option value="allowed">Allowed</option>
          <option value="required">Required</option>
        </select>
        <div style={PAGE_STYLES.note}>
          Whether guilds can opt into discovery listings.
        </div>
      </div>

      <div style={PAGE_STYLES.fieldRow}>
        <label style={PAGE_STYLES.label}>Max servers per user</label>
        <input
          type="number"
          className="input"
          min="0"
          value={maxServersPerUser}
          onChange={(e) => setMaxServersPerUser(e.target.value)}
          placeholder="No limit"
          style={{ maxWidth: '200px' }}
        />
        <div style={PAGE_STYLES.note}>
          Maximum number of servers a single user can own. Set to 0 to remove the limit.
        </div>
      </div>

      <div style={PAGE_STYLES.fieldRow}>
        <label style={PAGE_STYLES.label}>Max members per server</label>
        <input
          type="number"
          className="input"
          min="0"
          value={maxMembersPerServer}
          onChange={(e) => setMaxMembersPerServer(e.target.value)}
          placeholder="No limit"
          style={{ maxWidth: '200px' }}
        />
        <div style={PAGE_STYLES.note}>
          Maximum number of members allowed in a single server. Set to 0 to remove the limit.
        </div>
      </div>

      <div style={PAGE_STYLES.btnRow}>
        <button
          type="button"
          className="btn btn-primary"
          onClick={handleSave}
          disabled={saving}
        >
          {saving ? 'Saving...' : 'Save Configuration'}
        </button>
        {success && <span style={PAGE_STYLES.success}>{success}</span>}
        {error && <span style={PAGE_STYLES.error}>{error}</span>}
      </div>
    </div>
  );
}

// ─── Template Channel Editor ──────────────────────────────

function ChannelEditor({ channels, onChange }) {
  const [newName, setNewName] = useState('');
  const [newType, setNewType] = useState('text');

  const handleAdd = () => {
    const name = newName.trim();
    if (!name) return;
    const entry = { name, type: newType, position: channels.length };
    if (newType === 'voice') entry.voiceMode = 'quality';
    const updated = [...channels, entry];
    recalcPositions(updated);
    onChange(updated);
    setNewName('');
    setNewType('text');
  };

  const handleRemove = (idx) => {
    const updated = channels.filter((_, i) => i !== idx);
    recalcPositions(updated);
    onChange(updated);
  };

  const handleMove = (idx, dir) => {
    const updated = [...channels];
    const target = idx + dir;
    if (target < 0 || target >= updated.length) return;
    [updated[idx], updated[target]] = [updated[target], updated[idx]];
    recalcPositions(updated);
    onChange(updated);
  };

  function recalcPositions(chs) {
    let pos = 0;
    for (const ch of chs) {
      ch.position = ch.type === 'system' ? -1 : pos++;
    }
  }

  return (
    <div>
      <div style={{ marginBottom: '8px' }}>
        {channels.map((ch, idx) => (
          <div key={idx} style={PAGE_STYLES.channelRow}>
            <span style={PAGE_STYLES.channelType}>
              {CHANNEL_TYPE_ICONS[ch.type] || '?'} {ch.type}
            </span>
            <span style={PAGE_STYLES.channelName}>{ch.name}</span>
            {ch.type !== 'system' && (
              <>
                <button type="button" style={PAGE_STYLES.smallBtn} onClick={() => handleMove(idx, -1)} title="Move up">▲</button>
                <button type="button" style={PAGE_STYLES.smallBtn} onClick={() => handleMove(idx, 1)} title="Move down">▼</button>
                <button
                  type="button"
                  style={{ ...PAGE_STYLES.smallBtn, color: 'var(--danger)' }}
                  onClick={() => handleRemove(idx)}
                  title="Remove"
                >
                  ✕
                </button>
              </>
            )}
          </div>
        ))}
      </div>
      <div style={{ display: 'flex', gap: '8px', alignItems: 'center', flexWrap: 'wrap' }}>
        <input
          type="text"
          className="input"
          placeholder="Channel name"
          value={newName}
          onChange={(e) => setNewName(e.target.value)}
          onKeyDown={(e) => { if (e.key === 'Enter') handleAdd(); }}
          style={{ flex: '1 1 140px' }}
        />
        <select
          className="select"
          value={newType}
          onChange={(e) => setNewType(e.target.value)}
          style={{ width: '110px' }}
        >
          <option value="text">Text</option>
          <option value="voice">Voice</option>
          <option value="category">Category</option>
        </select>
        <button
          type="button"
          className="btn btn-secondary"
          style={{ padding: '6px 14px', fontSize: '0.8rem' }}
          onClick={handleAdd}
          disabled={!newName.trim()}
        >
          Add
        </button>
      </div>
    </div>
  );
}

// ─── Template Management Section ─────────────────────────

function TemplateSection() {
  const [templates, setTemplates] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [success, setSuccess] = useState('');
  const [editingId, setEditingId] = useState(null);
  const [editName, setEditName] = useState('');
  const [editChannels, setEditChannels] = useState([]);
  const [editIsDefault, setEditIsDefault] = useState(false);
  const [saving, setSaving] = useState(false);

  const reload = useCallback(async () => {
    try {
      const data = await listTemplates();
      setTemplates(Array.isArray(data) ? data : []);
    } catch (e) {
      setError(e.message);
    }
  }, []);

  useEffect(() => {
    let cancelled = false;
    listTemplates().then((data) => {
      if (!cancelled) setTemplates(Array.isArray(data) ? data : []);
    }).catch((e) => {
      if (!cancelled) setError(e.message);
    }).finally(() => {
      if (!cancelled) setLoading(false);
    });
    return () => { cancelled = true; };
  }, []);

  const startEdit = (tmpl) => {
    setEditingId(tmpl.id);
    setEditName(tmpl.name);
    setEditChannels(tmpl.channels.map((ch) => ({ ...ch })));
    setEditIsDefault(tmpl.isDefault);
    setError('');
    setSuccess('');
  };

  const startNew = () => {
    setEditingId('new');
    setEditName('');
    setEditChannels([...DEFAULT_TEMPLATE_CHANNELS]);
    setEditIsDefault(false);
    setError('');
    setSuccess('');
  };

  const cancelEdit = () => {
    setEditingId(null);
    setError('');
  };

  const handleSave = async () => {
    if (!editName.trim()) { setError('Template name is required'); return; }
    setSaving(true);
    setError('');
    setSuccess('');
    try {
      const body = { name: editName.trim(), channels: editChannels, isDefault: editIsDefault };
      if (editingId === 'new') {
        await createTemplate(body);
        setSuccess('Template created');
      } else {
        await updateTemplate(editingId, body);
        setSuccess('Template saved');
      }
      await reload();
      setEditingId(null);
      setTimeout(() => setSuccess(''), 3000);
    } catch (e) {
      setError(e.message);
    }
    setSaving(false);
  };

  const handleDelete = async (tmpl) => {
    if (!window.confirm(`Delete template "${tmpl.name}"?`)) return;
    setError('');
    try {
      await deleteTemplate(tmpl.id);
      await reload();
      setSuccess('Template deleted');
      setTimeout(() => setSuccess(''), 3000);
    } catch (e) {
      setError(e.message);
    }
  };

  if (loading) {
    return (
      <div style={PAGE_STYLES.section}>
        <div style={PAGE_STYLES.sectionTitle}>Server Templates</div>
        <div style={{ color: 'var(--text-muted)', fontSize: '0.85rem' }}>Loading templates...</div>
      </div>
    );
  }

  if (editingId) {
    return (
      <div style={PAGE_STYLES.section}>
        <div style={PAGE_STYLES.sectionTitle}>
          {editingId === 'new' ? 'New Template' : `Edit Template`}
        </div>
        <div style={PAGE_STYLES.editBox}>
          <div style={PAGE_STYLES.fieldRow}>
            <label style={PAGE_STYLES.label}>Template name</label>
            <input
              type="text"
              className="input"
              value={editName}
              onChange={(e) => setEditName(e.target.value)}
              placeholder="e.g. Gaming, Study Group"
              autoFocus
            />
          </div>
          <div style={{ ...PAGE_STYLES.fieldRow, display: 'flex', alignItems: 'center', gap: '8px' }}>
            <input
              type="checkbox"
              id="isDefault"
              checked={editIsDefault}
              onChange={(e) => setEditIsDefault(e.target.checked)}
            />
            <label htmlFor="isDefault" style={{ fontSize: '0.85rem', color: 'var(--text-secondary)', cursor: 'pointer' }}>
              Default template (used when no template is selected at guild creation)
            </label>
          </div>
          <div style={PAGE_STYLES.fieldRow}>
            <label style={PAGE_STYLES.label}>Channels</label>
            <ChannelEditor channels={editChannels} onChange={setEditChannels} />
          </div>
        </div>
        <div style={PAGE_STYLES.btnRow}>
          <button type="button" className="btn btn-primary" onClick={handleSave} disabled={saving}>
            {saving ? 'Saving...' : editingId === 'new' ? 'Create Template' : 'Save Changes'}
          </button>
          <button type="button" className="btn btn-secondary" onClick={cancelEdit} disabled={saving}>
            Cancel
          </button>
          {error && <span style={PAGE_STYLES.error}>{error}</span>}
        </div>
      </div>
    );
  }

  return (
    <div style={PAGE_STYLES.section}>
      <div style={PAGE_STYLES.sectionTitle}>Server Templates</div>

      {error && (
        <div style={{ ...PAGE_STYLES.error, padding: '10px 14px', background: 'rgba(239,68,68,0.08)', borderRadius: 'var(--radius-sm)', border: '1px solid rgba(239,68,68,0.2)', marginBottom: '12px' }}>
          {error}
        </div>
      )}

      {templates.length === 0 ? (
        <div style={{ color: 'var(--text-muted)', fontSize: '0.85rem', padding: '24px 0' }}>
          No templates yet. Create one to offer pre-configured channels at guild creation.
        </div>
      ) : (
        <div style={{ marginBottom: '12px' }}>
          {templates.map((tmpl) => (
            <div key={tmpl.id} style={{
              ...PAGE_STYLES.templateCard,
              ...(tmpl.isDefault ? { borderColor: 'rgba(34, 197, 94, 0.25)' } : {}),
            }}>
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={PAGE_STYLES.templateName}>
                  {tmpl.name}
                  {tmpl.isDefault && <span style={PAGE_STYLES.defaultBadge}>default</span>}
                </div>
                <div style={PAGE_STYLES.templateMeta}>
                  {tmpl.channels.length} channel{tmpl.channels.length !== 1 ? 's' : ''} &mdash;{' '}
                  {tmpl.channels
                    .filter((c) => c.type !== 'system')
                    .map((c) => (c.type === 'voice' ? `${c.name} (voice)` : `#${c.name}`))
                    .join(', ') || 'system channel only'}
                </div>
              </div>
              <button
                type="button"
                className="btn btn-secondary"
                style={{ padding: '4px 10px', fontSize: '0.78rem' }}
                onClick={() => startEdit(tmpl)}
              >
                Edit
              </button>
              <button
                type="button"
                style={{ ...PAGE_STYLES.smallBtn, color: 'var(--danger)', fontSize: '0.9rem' }}
                onClick={() => handleDelete(tmpl)}
                title="Delete template"
              >
                ✕
              </button>
            </div>
          ))}
        </div>
      )}

      <div style={{ display: 'flex', gap: '10px', alignItems: 'center' }}>
        <button type="button" className="btn btn-primary" onClick={startNew}>
          + New Template
        </button>
        {success && <span style={PAGE_STYLES.success}>{success}</span>}
      </div>
    </div>
  );
}

// ─── ConfigPage Root ──────────────────────────────────────

export default function ConfigPage() {
  return (
    <div style={PAGE_STYLES.container}>
      <InstanceConfigSection />
      <TemplateSection />
    </div>
  );
}
