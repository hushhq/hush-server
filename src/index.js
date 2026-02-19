import express from 'express';
import { createServer } from 'http';
import path from 'path';
import { fileURLToPath } from 'url';

import config from './config.js';
import { generateToken as generateLiveKitToken } from './livekit/tokenService.js';
import { listParticipants, removeParticipant } from './livekit/roomService.js';
import { getTotalRoomCount, deleteRoom, deleteRoomIfEmpty } from './synapseAdmin.js';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const app = express();
const httpServer = createServer(app);

// In-memory map for guest room expiry: roomId -> { roomName, createdAt (ms) }
const guestRoomsCreatedAt = new Map();
const GUEST_ROOM_EXPIRY_INTERVAL_MS = 60_000; // 1 min

// ─── Middleware ───────────────────────────────────────────
app.use(express.json());

app.use((req, res, next) => {
  res.header('Access-Control-Allow-Origin', config.corsOrigin);
  res.header('Access-Control-Allow-Methods', 'GET, POST');
  res.header('Access-Control-Allow-Headers', 'Content-Type, Authorization');
  next();
});

// ─── REST API ────────────────────────────────────────────

app.get('/api/health', (req, res) => {
  res.json({ status: 'ok', uptime: process.uptime() });
});

// Can the server accept a new guest room? (does not expose count or limit)
app.get('/api/rooms/can-create', async (req, res) => {
  try {
    const total = await getTotalRoomCount();
    if (total === null) {
      return res.status(503).json({
        allowed: false,
        reason:
          'Room availability check unavailable. Ensure SYNAPSE_ADMIN_TOKEN is set and Synapse is reachable.',
      });
    }
    if (total >= config.maxGuestRooms) {
      return res.json({ allowed: false, reason: 'All guest rooms are full.' });
    }
    return res.json({ allowed: true });
  } catch (err) {
    console.error('[api] can-create error:', err.message);
    return res.status(500).json({
      allowed: false,
      reason: 'Room availability check failed. Try again later.',
    });
  }
});

// Public limits for client (duration only; used for countdown)
app.get('/api/rooms/limits', (req, res) => {
  res.json({
    guestRoomMaxDurationMs: config.guestRoomMaxDurationMs,
  });
});

// Register a newly created guest room for expiry tracking
app.post('/api/rooms/created', (req, res) => {
  const { roomId, roomName, createdAt } = req.body;
  if (!roomId || !roomName || typeof createdAt !== 'number') {
    return res.status(400).json({ error: 'roomId, roomName, and createdAt (ms) required' });
  }
  guestRoomsCreatedAt.set(roomId, { roomName, createdAt });
  return res.json({ ok: true });
});

// LiveKit token (validates Matrix token via whoami; identity from Matrix)
app.post('/api/livekit/token', async (req, res) => {
  try {
    const authHeader = req.headers.authorization;
    const token = authHeader?.startsWith('Bearer ')
      ? authHeader.slice(7)
      : null;
    const { roomName, participantName } = req.body;

    if (!roomName) {
      return res.status(400).json({ error: 'roomName is required' });
    }

    let participants;
    try {
      participants = await listParticipants(roomName);
    } catch (listErr) {
      console.error('[api] listParticipants error:', listErr.message);
      return res.status(502).json({ error: 'Cannot check room. Try again.' });
    }
    if (participants.length >= config.maxParticipantsPerRoom) {
      return res.status(403).json({ error: 'This room is full.' });
    }

    const livekitToken = await generateLiveKitToken(
      token,
      roomName,
      participantName || 'Participant',
    );

    res.json({ token: livekitToken });
  } catch (err) {
    const status = err.statusCode || 500;
    console.error('[api] LiveKit token error:', err.message);
    res.status(status).json({ error: err.message || 'Token generation failed' });
  }
});

// Delete Matrix room from Synapse when empty (after last participant leaves)
app.post('/api/rooms/delete-if-empty', async (req, res) => {
  try {
    const { roomId } = req.body;
    if (!roomId) {
      return res.status(400).json({ error: 'roomId is required' });
    }
    const result = await deleteRoomIfEmpty(roomId);
    return res.json(result);
  } catch (err) {
    console.error('[api] delete-if-empty error:', err.message);
    return res.status(500).json({ error: err.message || 'Delete check failed' });
  }
});

// ─── Static files (production) ───────────────────────────
const clientBuild = path.join(__dirname, '../../client/dist');
app.use(express.static(clientBuild));
app.get('*', (req, res, next) => {
  if (req.path.startsWith('/api')) return next();
  res.sendFile(path.join(clientBuild, 'index.html'));
});

// ─── Guest room expiry job ───────────────────────────────
function runGuestRoomExpiry() {
  const now = Date.now();
  const maxDuration = config.guestRoomMaxDurationMs;
  for (const [roomId, { roomName, createdAt }] of guestRoomsCreatedAt.entries()) {
    if (now - createdAt < maxDuration) continue;
    (async () => {
      try {
        const participants = await listParticipants(roomName);
        for (const p of participants) {
          try {
            await removeParticipant(roomName, p.identity);
          } catch (e) {
            console.error('[expiry] removeParticipant:', e.message);
          }
        }
        const result = await deleteRoom(roomId);
        if (!result.ok) {
          console.error('[expiry] deleteRoom:', result.error);
        }
      } catch (err) {
        console.error('[expiry]', roomId, err.message);
      } finally {
        guestRoomsCreatedAt.delete(roomId);
      }
    })();
  }
}

// ─── Start ───────────────────────────────────────────────
async function start() {
  try {
    httpServer.listen(config.port, config.host, () => {
      console.log(`
╔══════════════════════════════════════╗
║             HUSH SERVER              ║
║──────────────────────────────────────║
║  http://${config.host}:${config.port}              ║
║  LiveKit + Matrix                    ║
╚══════════════════════════════════════╝
      `);
      setInterval(runGuestRoomExpiry, GUEST_ROOM_EXPIRY_INTERVAL_MS);
    });
  } catch (err) {
    console.error('[server] Failed to start:', err);
    process.exit(1);
  }
}

process.on('SIGINT', () => {
  console.log('\n[server] Shutting down...');
  httpServer.close();
  process.exit(0);
});

start();
