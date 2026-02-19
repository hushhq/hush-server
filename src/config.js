import 'dotenv/config';

const THREE_HOURS_MS = 3 * 60 * 60 * 1000;

const config = {
  port: parseInt(process.env.PORT || '3001'),
  host: process.env.HOST || '0.0.0.0',
  corsOrigin: process.env.CORS_ORIGIN || 'http://localhost:5173',

  maxGuestRooms: parseInt(process.env.MAX_GUEST_ROOMS || '30', 10),
  guestRoomMaxDurationMs: parseInt(
    process.env.GUEST_ROOM_MAX_DURATION_MS || String(THREE_HOURS_MS),
    10,
  ),
  maxParticipantsPerRoom: parseInt(
    process.env.MAX_PARTICIPANTS_PER_ROOM || '10',
    10,
  ),
};

export default config;
