import express from 'express';
import { createServer } from 'http';
import { Server as SocketIO } from 'socket.io';
import { v4 as uuidv4 } from 'uuid';
import path from 'path';
import { fileURLToPath } from 'url';

import config from './config.js';
import mediasoupManager from './media/mediasoupManager.js';
import roomManager from './rooms/roomManager.js';
import resourcePool from './rooms/resourcePool.js';
import { generateToken, socketAuthMiddleware } from './auth/auth.js';
import { registerSocketHandlers } from './signaling/socketHandlers.js';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const app = express();
const httpServer = createServer(app);

// ─── Middleware ───────────────────────────────────────────
app.use(express.json());

// CORS for development
app.use((req, res, next) => {
  res.header('Access-Control-Allow-Origin', config.corsOrigin);
  res.header('Access-Control-Allow-Methods', 'GET, POST');
  res.header('Access-Control-Allow-Headers', 'Content-Type, Authorization');
  next();
});

// ─── REST API ────────────────────────────────────────────

// Health check
app.get('/api/health', (req, res) => {
  res.json({
    status: 'ok',
    rooms: roomManager.getRoomList().length,
    uptime: process.uptime(),
  });
});

// Public server status — fully transparent resource allocation
// Anyone can verify these numbers match the source code
app.get('/api/status', (req, res) => {
  res.json({
    status: 'ok',
    pools: resourcePool.getPublicStatus(),
    system: resourcePool.getSystemInfo(),
    rooms: roomManager.getRoomList(),
  });
});

// List rooms (names and participant counts only)
app.get('/api/rooms', (req, res) => {
  res.json({
    rooms: roomManager.getRoomList(),
    pools: resourcePool.getPublicStatus(),
  });
});

// Create room
app.post('/api/rooms/create', async (req, res) => {
  try {
    const { roomName, password, displayName, tier } = req.body;

    if (!roomName || !password || !displayName) {
      return res.status(400).json({ error: 'roomName, password, and displayName required' });
    }

    if (roomName.length > 50 || password.length < 4) {
      return res.status(400).json({ error: 'Room name max 50 chars, password min 4 chars' });
    }

    const existingRoom = roomManager.getRoom(roomName);
    if (existingRoom) {
      return res.status(409).json({ error: 'Room already exists' });
    }

    // Determine tier (default: free)
    // TODO: validate supporter tier against actual payment/auth
    const roomTier = tier === 'supporter' ? 'supporter' : 'free';

    const peerId = uuidv4();
    const room = await roomManager.createRoom(roomName, password, peerId, roomTier);
    roomManager.addPeer(roomName, peerId, displayName);

    const token = generateToken(peerId, roomName, displayName);

    res.json({
      token,
      peerId,
      roomName,
      tier: roomTier,
      limits: room.limits,
    });
  } catch (err) {
    // Handle pool-full errors with transparent messaging
    if (err.code === 'free_pool_full') {
      return res.status(503).json({
        error: 'FREE_POOL_FULL',
        message: 'I posti gratuiti sono esauriti in questo momento.',
        pools: err.pool,
        options: [
          { action: 'retry', label: 'Riprova tra qualche minuto' },
          { action: 'support', label: 'Accesso dedicato con supporto' },
          { action: 'selfhost', label: 'Self-hosting (gratis, illimitato)' },
        ],
      });
    }
    if (err.code === 'supporter_pool_full') {
      return res.status(503).json({
        error: 'SUPPORTER_POOL_FULL',
        message: 'Anche i posti supporter sono temporaneamente esauriti.',
        pools: err.pool,
      });
    }

    console.error('[api] Create room error:', err);
    res.status(500).json({ error: err.message });
  }
});

// Join room
app.post('/api/rooms/join', async (req, res) => {
  try {
    const { roomName, password, displayName } = req.body;

    if (!roomName || !password || !displayName) {
      return res.status(400).json({ error: 'roomName, password, and displayName required' });
    }

    const room = roomManager.getRoom(roomName);
    if (!room) {
      return res.status(404).json({ error: 'Room not found' });
    }

    const valid = await roomManager.verifyPassword(roomName, password);
    if (!valid) {
      return res.status(401).json({ error: 'Incorrect password' });
    }

    const peerId = uuidv4();
    roomManager.addPeer(roomName, peerId, displayName);

    const token = generateToken(peerId, roomName, displayName);

    res.json({
      token,
      peerId,
      roomName,
    });
  } catch (err) {
    if (err.message === 'Room is full') {
      return res.status(403).json({ error: err.message });
    }
    console.error('[api] Join room error:', err);
    res.status(500).json({ error: err.message });
  }
});

// ─── Static files (production) ───────────────────────────
const clientBuild = path.join(__dirname, '../../client/dist');
app.use(express.static(clientBuild));
app.get('*', (req, res, next) => {
  // Don't catch API routes
  if (req.path.startsWith('/api')) return next();
  res.sendFile(path.join(clientBuild, 'index.html'));
});

// ─── Socket.io ───────────────────────────────────────────
const io = new SocketIO(httpServer, {
  cors: {
    origin: config.corsOrigin,
    methods: ['GET', 'POST'],
  },
  // Optimize for media signaling
  pingTimeout: 30000,
  pingInterval: 10000,
});

// Auth middleware — verify JWT before allowing socket connection
io.use(socketAuthMiddleware);

// Register all signaling handlers
registerSocketHandlers(io);

// ─── Start ───────────────────────────────────────────────
async function start() {
  try {
    // Initialize mediasoup workers
    await mediasoupManager.init();

    httpServer.listen(config.port, config.host, () => {
      console.log(`
╔══════════════════════════════════════╗
║             HUSH SERVER              ║
║──────────────────────────────────────║
║  http://${config.host}:${config.port}              ║
║  mediasoup workers: ${config.mediasoup.numWorkers}               ║
║  max per room: ${config.maxParticipantsPerRoom}                  ║
╚══════════════════════════════════════╝
      `);
    });
  } catch (err) {
    console.error('[server] Failed to start:', err);
    process.exit(1);
  }
}

// Graceful shutdown
process.on('SIGINT', () => {
  console.log('\n[server] Shutting down...');
  mediasoupManager.close();
  httpServer.close();
  process.exit(0);
});

start();
