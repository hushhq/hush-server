/**
 * Synapse Admin API helpers.
 * Used to delete empty rooms when the last participant leaves.
 * Requires SYNAPSE_ADMIN_TOKEN (admin access token) and MATRIX_HOMESERVER_URL.
 */

const MATRIX_HOMESERVER_URL =
  process.env.MATRIX_HOMESERVER_URL || 'http://localhost:8008';
const SYNAPSE_ADMIN_TOKEN = process.env.SYNAPSE_ADMIN_TOKEN;

const BASE = MATRIX_HOMESERVER_URL.replace(/\/$/, '');

/**
 * Returns total number of rooms on the server (for room limit enforcement).
 * Uses Synapse Admin API list rooms with limit=1 to get total_rooms from response.
 *
 * @returns {Promise<number | null>} Total room count, or null if API unavailable / token not set
 */
export async function getTotalRoomCount() {
  if (!SYNAPSE_ADMIN_TOKEN) return null;
  const url = `${BASE}/_synapse/admin/v1/rooms?limit=1`;
  try {
    const res = await fetch(url, {
      method: 'GET',
      headers: {
        Authorization: `Bearer ${SYNAPSE_ADMIN_TOKEN}`,
        'Content-Type': 'application/json',
      },
    });
    if (!res.ok) return null;
    const data = await res.json();
    return typeof data.total_rooms === 'number' ? data.total_rooms : null;
  } catch {
    return null;
  }
}

/**
 * List all rooms on the Synapse server.
 * @returns {Promise<Array<{ room_id: string, name: string, canonical_alias: string, joined_members: number }>>}
 */
export async function listAllRooms() {
  if (!SYNAPSE_ADMIN_TOKEN) return [];
  const url = `${BASE}/_synapse/admin/v1/rooms?limit=500`;
  try {
    const res = await fetch(url, {
      method: 'GET',
      headers: {
        Authorization: `Bearer ${SYNAPSE_ADMIN_TOKEN}`,
        'Content-Type': 'application/json',
      },
    });
    if (!res.ok) return [];
    const data = await res.json();
    return data.rooms || [];
  } catch {
    return [];
  }
}

/**
 * @param {string} roomId - Matrix room ID (!xxx:server)
 * @returns {Promise<{ joined_members: number } | null>} Room details or null if not found/error
 */
export async function getRoomDetails(roomId) {
  if (!SYNAPSE_ADMIN_TOKEN) return null;
  const url = `${BASE}/_synapse/admin/v1/rooms/${encodeURIComponent(roomId)}`;
  try {
    const res = await fetch(url, {
      method: 'GET',
      headers: {
        Authorization: `Bearer ${SYNAPSE_ADMIN_TOKEN}`,
        'Content-Type': 'application/json',
      },
    });
    if (!res.ok) return null;
    const data = await res.json();
    return { joined_members: data.joined_members ?? 0 };
  } catch {
    return null;
  }
}

/**
 * Delete a room via Synapse Admin API (purge from DB).
 * Only safe when room has 0 members; use getRoomDetails first.
 *
 * @param {string} roomId - Matrix room ID
 * @returns {Promise<{ ok: boolean, error?: string }>}
 */
export async function deleteRoom(roomId) {
  if (!SYNAPSE_ADMIN_TOKEN) {
    return { ok: false, error: 'SYNAPSE_ADMIN_TOKEN not configured' };
  }
  const url = `${BASE}/_synapse/admin/v2/rooms/${encodeURIComponent(roomId)}`;
  try {
    const res = await fetch(url, {
      method: 'DELETE',
      headers: {
        Authorization: `Bearer ${SYNAPSE_ADMIN_TOKEN}`,
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({ purge: true }),
    });
    if (!res.ok) {
      const text = await res.text();
      return { ok: false, error: text || res.statusText };
    }
    return { ok: true };
  } catch (err) {
    return { ok: false, error: err.message };
  }
}

/**
 * If the room has 0 members, delete it from Synapse.
 *
 * @param {string} roomId - Matrix room ID
 * @returns {Promise<{ deleted: boolean, reason?: string }>}
 */
export async function deleteRoomIfEmpty(roomId) {
  if (!roomId || typeof roomId !== 'string' || !roomId.startsWith('!')) {
    return { deleted: false, reason: 'invalid room id' };
  }
  const details = await getRoomDetails(roomId);
  if (!details) {
    return { deleted: false, reason: 'room not found or admin API unavailable' };
  }
  if (details.joined_members > 0) {
    return { deleted: false, reason: 'room still has members' };
  }
  const result = await deleteRoom(roomId);
  if (!result.ok) {
    return { deleted: false, reason: result.error || 'delete failed' };
  }
  return { deleted: true };
}
