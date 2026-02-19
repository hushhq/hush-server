/**
 * LiveKit token service: issues a JWT after validating the caller's Matrix identity.
 *
 * Room behaviour (post-B3): This service does NOT validate Matrix room membership.
 * It validates only the Matrix access token (whoami). Access control is enforced by
 * Matrix (the user must be in the room to know the room name) and by the room name
 * acting as a shared secret. See SECURITY.md for full documentation.
 */
import { AccessToken } from 'livekit-server-sdk';

const MATRIX_HOMESERVER_URL =
  process.env.MATRIX_HOMESERVER_URL || 'http://localhost:8008';

/**
 * Validates a Matrix access token via Synapse whoami.
 * @param {string} matrixAccessToken - Bearer token from Authorization header
 * @returns {Promise<{ userId: string }>} whoami result
 * @throws {Error} If token is missing or invalid
 */
async function validateMatrixToken(matrixAccessToken) {
  if (!matrixAccessToken?.trim()) {
    const err = new Error('Matrix access token required');
    err.statusCode = 401;
    throw err;
  }

  const url = `${MATRIX_HOMESERVER_URL.replace(/\/$/, '')}/_matrix/client/v3/account/whoami`;
  let res;
  try {
    res = await fetch(url, {
      method: 'GET',
      headers: {
        Authorization: `Bearer ${matrixAccessToken.trim()}`,
        'Content-Type': 'application/json',
      },
    });
  } catch (fetchErr) {
    console.error('[tokenService] Synapse whoami request failed:', fetchErr.message);
    const err = new Error('Cannot reach Matrix server. Is Synapse running?');
    err.statusCode = 502;
    throw err;
  }

  if (!res.ok) {
    const err = new Error(res.status === 401 ? 'Invalid Matrix token' : 'Matrix whoami failed');
    err.statusCode = res.status === 401 ? 401 : 502;
    throw err;
  }

  let data;
  try {
    data = await res.json();
  } catch (parseErr) {
    console.error('[tokenService] Invalid whoami response body:', parseErr.message);
    const err = new Error('Invalid Matrix server response');
    err.statusCode = 502;
    throw err;
  }

  if (!data?.user_id) {
    const err = new Error('Invalid whoami response');
    err.statusCode = 502;
    throw err;
  }

  return { userId: data.user_id };
}

/**
 * Generates a LiveKit access token for a participant after validating Matrix identity.
 * Identity is taken from Matrix whoami, not from request body.
 *
 * @param {string} matrixAccessToken - Matrix access token (Bearer)
 * @param {string} roomName - The name of the LiveKit room
 * @param {string} participantName - Display name for the participant
 * @returns {string} JWT token for LiveKit access
 * @throws {Error} If token invalid or env missing
 */
export async function generateToken(matrixAccessToken, roomName, participantName) {
  const apiKey = process.env.LIVEKIT_API_KEY;
  const apiSecret = process.env.LIVEKIT_API_SECRET;

  if (!apiKey || !apiSecret) {
    console.error('[tokenService] LIVEKIT_API_KEY or LIVEKIT_API_SECRET not set');
    const err = new Error('Server misconfiguration: LiveKit keys missing');
    err.statusCode = 500;
    throw err;
  }

  const { userId } = await validateMatrixToken(matrixAccessToken);

  const token = new AccessToken(apiKey, apiSecret, {
    identity: userId,
    name: participantName || userId,
  });

  token.addGrant({
    room: roomName,
    roomJoin: true,
    canPublish: true,
    canSubscribe: true,
    canPublishData: true,
  });

  return await token.toJwt();
}
