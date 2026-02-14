import { AccessToken } from 'livekit-server-sdk';

/**
 * Generates a LiveKit access token for a participant to join a room.
 *
 * @param {string} roomName - The name of the LiveKit room
 * @param {string} participantIdentity - Unique identifier for the participant
 * @param {string} participantName - Display name for the participant
 * @returns {string} JWT token for LiveKit access
 * @throws {Error} If required environment variables are missing
 */
export async function generateToken(roomName, participantIdentity, participantName) {
  const apiKey = process.env.LIVEKIT_API_KEY;
  const apiSecret = process.env.LIVEKIT_API_SECRET;

  if (!apiKey || !apiSecret) {
    throw new Error('LIVEKIT_API_KEY and LIVEKIT_API_SECRET must be set');
  }

  const token = new AccessToken(apiKey, apiSecret, {
    identity: participantIdentity,
    name: participantName,
  });

  token.addGrant({
    room: roomName,
    roomJoin: true,
    canPublish: true,
    canSubscribe: true,
    canPublishData: true,
  });

  // toJwt() returns a Promise in livekit-server-sdk v2.x
  return await token.toJwt();
}
