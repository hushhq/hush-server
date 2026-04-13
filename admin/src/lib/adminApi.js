const BASE = '/api/admin';

async function parseError(response) {
  try {
    const body = await response.json();
    return body?.error || `HTTP ${response.status}`;
  } catch {
    return `HTTP ${response.status}`;
  }
}

async function request(path, options = {}) {
  const response = await fetch(`${BASE}${path}`, {
    credentials: 'same-origin',
    headers: {
      'Content-Type': 'application/json',
      ...(options.headers || {}),
    },
    ...options,
  });

  if (!response.ok) {
    const error = new Error(await parseError(response));
    error.status = response.status;
    throw error;
  }
  return response;
}

export async function getBootstrapStatus() {
  const response = await request('/bootstrap/status', { method: 'POST' });
  return response.json();
}

export async function claimBootstrap(payload) {
  const response = await request('/bootstrap/claim', {
    method: 'POST',
    body: JSON.stringify(payload),
  });
  return response.json();
}

export async function loginAdmin(payload) {
  const response = await request('/session/login', {
    method: 'POST',
    body: JSON.stringify(payload),
  });
  return response.json();
}

export async function logoutAdmin() {
  await request('/session/logout', { method: 'POST' });
}

export async function getCurrentAdmin() {
  const response = await request('/session/me');
  return response.json();
}

export async function listGuilds() {
  const response = await request('/guilds');
  return response.json();
}

export async function listUsers() {
  const response = await request('/users');
  return response.json();
}

export async function getHealth() {
  const response = await request('/health');
  return response.json();
}

export async function getConfig() {
  const response = await request('/config');
  return response.json();
}

export async function updateConfig(updates) {
  const response = await request('/config', {
    method: 'PUT',
    body: JSON.stringify(updates),
  });
  if (response.status === 204) {
    return {};
  }
  return response.json();
}

export async function listTemplates() {
  const response = await request('/templates');
  return response.json();
}

export async function createTemplate(data) {
  const response = await request('/templates', {
    method: 'POST',
    body: JSON.stringify(data),
  });
  return response.json();
}

export async function updateTemplate(id, data) {
  const response = await request(`/templates/${encodeURIComponent(id)}`, {
    method: 'PUT',
    body: JSON.stringify(data),
  });
  if (response.status === 204) {
    return {};
  }
  return response.json();
}

export async function deleteTemplate(id) {
  await request(`/templates/${encodeURIComponent(id)}`, { method: 'DELETE' });
}

export async function instanceBan(userId, reason, expiresAt) {
  const body = { userId, reason };
  if (expiresAt !== null && expiresAt !== undefined) {
    body.expiresIn = Math.max(0, Math.floor(expiresAt - Date.now() / 1000));
  }
  await request('/bans', {
    method: 'POST',
    body: JSON.stringify(body),
  });
}

export async function instanceUnban(userId, reason) {
  await request(`/bans/${encodeURIComponent(userId)}`, {
    method: 'DELETE',
    body: JSON.stringify({ reason }),
  });
}

export async function getAuditLog(options = {}) {
  const params = new URLSearchParams();
  if (options.limit != null) params.set('limit', String(options.limit));
  if (options.offset != null) params.set('offset', String(options.offset));
  if (options.action) params.set('action', options.action);
  if (options.targetId) params.set('targetId', options.targetId);
  const response = await request(`/audit-log${params.toString() ? `?${params}` : ''}`);
  return response.json();
}

export async function getServiceIdentity() {
  const response = await request('/service-identity');
  return response.json();
}

export async function setGuildMemberCap(serverId, memberCapOverride) {
  const response = await request(`/guilds/${encodeURIComponent(serverId)}/member-cap`, {
    method: 'PUT',
    body: JSON.stringify({ memberCapOverride }),
  });
  return response.json();
}
