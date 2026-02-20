import express from 'express';
import { createServer } from 'http';
import path from 'path';
import fs from 'fs';
import { fileURLToPath } from 'url';

import config from './config.js';
import { generateToken as generateLiveKitToken } from './livekit/tokenService.js';
import { listParticipants, listRooms, removeParticipant } from './livekit/roomService.js';
import { getTotalRoomCount, listAllRooms, deleteRoom, deleteRoomIfEmpty } from './synapseAdmin.js';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const app = express();
const httpServer = createServer(app);

// In-memory map for guest room expiry: roomId -> { roomName, createdAt (ms) }
const guestRoomsCreatedAt = new Map();
const GUEST_ROOM_EXPIRY_INTERVAL_MS = 5_000; // 5 sec

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
const indexHtml = fs.readFileSync(path.join(clientBuild, 'index.html'), 'utf-8');
app.use(express.static(clientBuild));
app.get('*', (req, res, next) => {
  if (req.path.startsWith('/api')) return next();

  // Inject dynamic OG meta tags for room links so social previews show room info
  const roomMatch = req.path.match(/^\/room\/([^/]+)/);
  if (roomMatch) {
    const rawName = decodeURIComponent(roomMatch[1]);
    // Strip the 8-hex suffix to get the display name: "my-room-a1b2c3d4" → "my-room"
    const displayName = rawName.replace(/-[a-f0-9]{8}$/, '') || rawName;
    const safeDisplay = displayName.replace(/[<>"&]/g, '');
    const html = indexHtml
      .replace(
        /<meta property="og:title"[^>]*>/,
        `<meta property="og:title" content="join ${safeDisplay} on hush">`,
      )
      .replace(
        /<meta property="og:description"[^>]*>/,
        `<meta property="og:description" content="You've been invited to a private screen-sharing room. Tap to join.">`,
      )
      .replace(
        /<title>[^<]*<\/title>/,
        `<title>${safeDisplay} — hush</title>`,
      );
    return res.send(html);
  }

  res.sendFile(path.join(clientBuild, 'index.html'));
});

// ─── Guest room expiry (max duration) ────────────────────
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
        } else {
          console.log('[expiry] Deleted expired room:', roomName);
        }
      } catch (err) {
        console.error('[expiry]', roomId, err.message);
      } finally {
        guestRoomsCreatedAt.delete(roomId);
      }
    })();
  }
}

// ─── Orphan room cleanup ─────────────────────────────────
// Deletes Matrix rooms that have no active LiveKit participants AND no Matrix members.
// A room is only an orphan if both conditions are true — this prevents deleting rooms
// where users are connected via Matrix but haven't joined LiveKit yet (e.g. link-join flow).
async function runOrphanRoomCleanup() {
  try {
    const [synapseRooms, livekitRooms] = await Promise.all([
      listAllRooms(),
      listRooms(),
    ]);
    if (synapseRooms.length === 0) return;

    // Build set of LiveKit room names that have active participants
    const activeRoomNames = new Set(
      livekitRooms
        .filter((r) => r.numParticipants > 0)
        .map((r) => r.name),
    );

    for (const room of synapseRooms) {
      // Derive LiveKit room name from alias: "#roomname:server" → "roomname"
      const alias = room.canonical_alias || '';
      const roomName = alias.replace(/^#/, '').replace(/:.*$/, '');
      if (!roomName) continue;

      // Skip rooms that have active LiveKit participants
      if (activeRoomNames.has(roomName)) continue;

      // Skip rooms that still have Matrix members (e.g. user joined Matrix but not yet on LiveKit)
      if (room.joined_members > 0) continue;

      const result = await deleteRoom(room.room_id);
      if (result.ok) {
        console.log('[cleanup] Deleted orphan room:', roomName, room.room_id);
        guestRoomsCreatedAt.delete(room.room_id);
      } else {
        console.error('[cleanup] Failed to delete:', roomName, result.error);
      }
    }
  } catch (err) {
    console.error('[cleanup] Orphan room cleanup error:', err.message);
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
      setInterval(runOrphanRoomCleanup, GUEST_ROOM_EXPIRY_INTERVAL_MS);
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
