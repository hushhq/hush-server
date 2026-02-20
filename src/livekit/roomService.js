/**
 * LiveKit Room Service API client.
 * Used for listing participants (max-per-room check) and removing participants (room expiry).
 */
import { RoomServiceClient } from 'livekit-server-sdk';

const LIVEKIT_URL = process.env.LIVEKIT_URL || 'ws://localhost:7880';
const apiKey = process.env.LIVEKIT_API_KEY;
const apiSecret = process.env.LIVEKIT_API_SECRET;

/** @type {RoomServiceClient | null} */
let _client = null;

/**
 * LiveKit server API uses HTTP(S). Convert ws(s) URL to http(s) and strip path.
 * @returns {string} Base URL for RoomServiceClient (e.g. http://localhost:7880)
 */
function getLiveKitHost() {
  const u = LIVEKIT_URL.replace(/^ws:/i, 'http:').replace(/^wss:/i, 'https:');
  try {
    const parsed = new URL(u);
    return `${parsed.protocol}//${parsed.host}`;
  } catch {
    return 'http://localhost:7880';
  }
}

/**
 * Lazy singleton RoomServiceClient. Returns null if API key/secret not set.
 * @returns {RoomServiceClient | null}
 */
function getRoomServiceClient() {
  if (!apiKey || !apiSecret) return null;
  if (!_client) {
    const host = getLiveKitHost();
    _client = new RoomServiceClient(host, apiKey, apiSecret);
  }
  return _client;
}

/**
 * List all active LiveKit rooms.
 * @returns {Promise<Array<{ name: string, numParticipants: number }>>}
 */
export async function listRooms() {
  const client = getRoomServiceClient();
  if (!client) return [];
  const rooms = await client.listRooms();
  return rooms.map((r) => ({ name: r.name, numParticipants: r.numParticipants }));
}

/**
 * List participants in a LiveKit room.
 *
 * @param {string} roomName - LiveKit room name
 * @returns {Promise<Array<{ identity: string }>>} Participants (identity only for removeParticipant)
 * @throws {Error} If LiveKit keys missing or API call fails
 */
export async function listParticipants(roomName) {
  const client = getRoomServiceClient();
  if (!client) {
    throw new Error('LiveKit API keys not configured');
  }
  const participants = await client.listParticipants(roomName);
  return participants.map((p) => ({ identity: p.identity }));
}

/**
 * Remove a participant from a LiveKit room (disconnects them).
 *
 * @param {string} roomName - LiveKit room name
 * @param {string} identity - Participant identity
 * @throws {Error} If LiveKit keys missing or API call fails
 */
export async function removeParticipant(roomName, identity) {
  const client = getRoomServiceClient();
  if (!client) {
    throw new Error('LiveKit API keys not configured');
  }
  await client.removeParticipant(roomName, identity);
}
