import { useCallback, useEffect, useState } from 'react';
import { BrowserRouter, Routes, Route, NavLink, Navigate } from 'react-router-dom';
import GuildListPage from './pages/GuildListPage.jsx';
import UserListPage from './pages/UserListPage.jsx';
import ConfigPage from './pages/ConfigPage.jsx';
import HealthPage from './pages/HealthPage.jsx';
import {
  claimBootstrap,
  getBootstrapStatus,
  getCurrentAdmin,
  logoutAdmin,
  loginAdmin,
} from './lib/adminApi.js';

const styles = {
  shell: {
    display: 'flex',
    flexDirection: 'column',
    height: '100vh',
    overflow: 'hidden',
  },
  header: {
    display: 'flex',
    alignItems: 'center',
    gap: '24px',
    padding: '0 24px',
    height: '52px',
    background: 'var(--surface)',
    borderBottom: '1px solid var(--border)',
    flexShrink: 0,
  },
  brand: {
    fontSize: '0.95rem',
    fontWeight: 700,
    color: 'var(--accent)',
    letterSpacing: '0.02em',
  },
  nav: {
    display: 'flex',
    gap: '4px',
    flex: 1,
    minWidth: 0,
  },
  navLink: {
    padding: '6px 12px',
    borderRadius: 'var(--radius-sm)',
    fontSize: '0.85rem',
    color: 'var(--text-secondary)',
    fontWeight: 400,
    textDecoration: 'none',
    transition: 'color 0.12s ease, background 0.12s ease',
  },
  body: {
    flex: 1,
    overflow: 'hidden',
    display: 'flex',
    flexDirection: 'column',
  },
  gate: {
    display: 'flex',
    flexDirection: 'column',
    alignItems: 'center',
    justifyContent: 'center',
    height: '100%',
    gap: '16px',
    padding: '40px',
  },
  gateBox: {
    display: 'flex',
    flexDirection: 'column',
    gap: '14px',
    width: '360px',
    maxWidth: '100%',
  },
  gateTitle: {
    fontSize: '1.1rem',
    fontWeight: 600,
    color: 'var(--text)',
    marginBottom: '4px',
  },
  gateNote: {
    fontSize: '0.85rem',
    color: 'var(--text-muted)',
    maxWidth: '420px',
    textAlign: 'center',
    lineHeight: 1.6,
  },
  meta: {
    display: 'flex',
    alignItems: 'center',
    gap: '10px',
    color: 'var(--text-muted)',
    fontSize: '0.78rem',
  },
  controls: {
    display: 'flex',
    alignItems: 'center',
    gap: '12px',
    marginLeft: 'auto',
    flexShrink: 0,
  },
  badge: {
    display: 'inline-flex',
    padding: '2px 8px',
    borderRadius: '999px',
    background: 'rgba(213, 79, 18, 0.15)',
    color: 'var(--accent)',
    fontWeight: 600,
    textTransform: 'uppercase',
    letterSpacing: '0.05em',
    fontSize: '0.68rem',
  },
  logoutBtn: {
    background: 'none',
    border: '1px solid var(--border)',
    borderRadius: 'var(--radius-sm)',
    color: 'var(--text-muted)',
    cursor: 'pointer',
    padding: '4px 12px',
    fontSize: '0.78rem',
    transition: 'color 0.12s ease, border-color 0.12s ease',
  },
  error: {
    color: 'var(--danger)',
    fontSize: '0.82rem',
  },
};

function BootstrapGate({ onClaim }) {
  const [username, setUsername] = useState('');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [bootstrapSecret, setBootstrapSecret] = useState('');
  const [error, setError] = useState('');
  const [submitting, setSubmitting] = useState(false);

  const handleSubmit = async (event) => {
    event.preventDefault();
    setSubmitting(true);
    setError('');
    try {
      const response = await claimBootstrap({
        username: username.trim(),
        email: email.trim() || undefined,
        password,
        bootstrapSecret: bootstrapSecret.trim(),
      });
      onClaim(response.admin);
    } catch (cause) {
      setError(cause.message);
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div style={styles.gate}>
      <div style={styles.gateTitle}>Bootstrap Hush Admin</div>
      <div style={styles.gateNote}>
        No instance admin exists yet. Create the first owner account using the one-time bootstrap secret.
      </div>
      <form style={styles.gateBox} onSubmit={handleSubmit}>
        <input className="input" type="text" placeholder="Owner username" value={username} onChange={(event) => setUsername(event.target.value)} autoFocus />
        <input className="input" type="email" placeholder="Email (optional)" value={email} onChange={(event) => setEmail(event.target.value)} />
        <input className="input" type="password" placeholder="Password (min 12 chars)" value={password} onChange={(event) => setPassword(event.target.value)} />
        <input className="input" type="password" placeholder="Bootstrap secret" value={bootstrapSecret} onChange={(event) => setBootstrapSecret(event.target.value)} />
        {error && <div style={styles.error}>{error}</div>}
        <button
          type="submit"
          className="btn btn-primary"
          disabled={submitting || !username.trim() || !password || !bootstrapSecret.trim()}
        >
          {submitting ? 'Creating owner...' : 'Create owner'}
        </button>
      </form>
    </div>
  );
}

function LoginGate({ onLogin }) {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [submitting, setSubmitting] = useState(false);

  const handleSubmit = async (event) => {
    event.preventDefault();
    setSubmitting(true);
    setError('');
    try {
      const response = await loginAdmin({ username: username.trim(), password });
      onLogin(response.admin);
    } catch (cause) {
      setError(cause.message);
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div style={styles.gate}>
      <div style={styles.gateTitle}>Hush Instance Admin</div>
      <div style={styles.gateNote}>
        Sign in with a local instance-admin account. The dashboard uses a secure server-issued session cookie.
      </div>
      <form style={styles.gateBox} onSubmit={handleSubmit}>
        <input className="input" type="text" placeholder="Username" value={username} onChange={(event) => setUsername(event.target.value)} autoFocus />
        <input className="input" type="password" placeholder="Password" value={password} onChange={(event) => setPassword(event.target.value)} />
        {error && <div style={styles.error}>{error}</div>}
        <button type="submit" className="btn btn-primary" disabled={submitting || !username.trim() || !password}>
          {submitting ? 'Signing in...' : 'Sign in'}
        </button>
      </form>
    </div>
  );
}

function LoadingGate({ note }) {
  return (
    <div style={styles.gate}>
      <div style={styles.gateTitle}>Hush Instance Admin</div>
      <div style={styles.gateNote}>{note}</div>
    </div>
  );
}

function AdminShell({ admin, onLogout }) {
  const navActive = ({ isActive }) => ({
    ...styles.navLink,
    ...(isActive ? { color: 'var(--text)', background: 'var(--elevated)', fontWeight: 500 } : {}),
  });

  return (
    <div style={styles.shell}>
      <header className="admin-header" style={styles.header}>
        <span className="admin-brand" style={styles.brand}>Hush Admin</span>
        <nav className="admin-nav" style={styles.nav}>
          <NavLink to="/guilds" style={navActive}>Guilds</NavLink>
          <NavLink to="/users" style={navActive}>Users</NavLink>
          <NavLink to="/config" style={navActive}>Config</NavLink>
          <NavLink to="/health" style={navActive}>Health</NavLink>
        </nav>
        <div className="admin-controls" style={styles.controls}>
          <div className="admin-meta" style={styles.meta}>
            <span className="admin-username">{admin.username}</span>
            <span style={styles.badge}>{admin.role}</span>
          </div>
          <button className="admin-logout" type="button" style={styles.logoutBtn} onClick={onLogout}>
            Logout
          </button>
        </div>
      </header>
      <div className="admin-body" style={styles.body}>
        <Routes>
          <Route path="/" element={<Navigate to="/guilds" replace />} />
          <Route path="/guilds" element={<GuildListPage />} />
          <Route path="/users" element={<UserListPage />} />
          <Route path="/config" element={<ConfigPage />} />
          <Route path="/health" element={<HealthPage />} />
        </Routes>
      </div>
    </div>
  );
}

export default function App() {
  const [screen, setScreen] = useState('loading');
  const [admin, setAdmin] = useState(null);

  const restoreSession = useCallback(async () => {
    try {
      const bootstrapStatus = await getBootstrapStatus();
      if (bootstrapStatus.bootstrapAvailable) {
        setScreen('bootstrap');
        return;
      }
      const response = await getCurrentAdmin();
      setAdmin(response.admin);
      setScreen('ready');
    } catch (cause) {
      if (cause.status === 401) {
        setScreen('login');
        return;
      }
      setScreen('error');
    }
  }, []);

  useEffect(() => {
    restoreSession();
  }, [restoreSession]);

  const handleBootstrap = useCallback((currentAdmin) => {
    setAdmin(currentAdmin);
    setScreen('ready');
  }, []);

  const handleLogin = useCallback((currentAdmin) => {
    setAdmin(currentAdmin);
    setScreen('ready');
  }, []);

  const handleLogout = useCallback(async () => {
    await logoutAdmin();
    setAdmin(null);
    setScreen('login');
  }, []);

  return (
    <BrowserRouter basename="/admin">
      {screen === 'loading' && <LoadingGate note="Checking bootstrap state and existing admin session..." />}
      {screen === 'bootstrap' && <BootstrapGate onClaim={handleBootstrap} />}
      {screen === 'login' && <LoginGate onLogin={handleLogin} />}
      {screen === 'error' && <LoadingGate note="The admin control plane could not be loaded. Check the server and try again." />}
      {screen === 'ready' && admin && <AdminShell admin={admin} onLogout={handleLogout} />}
    </BrowserRouter>
  );
}
