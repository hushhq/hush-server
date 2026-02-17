import express from 'express';
import { createServer } from 'http';
import path from 'path';
import { fileURLToPath } from 'url';

import config from './config.js';
import { generateToken as generateLiveKitToken } from './livekit/tokenService.js';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const app = express();
const httpServer = createServer(app);

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

// ─── Static files (production) ───────────────────────────
const clientBuild = path.join(__dirname, '../../client/dist');
app.use(express.static(clientBuild));
app.get('*', (req, res, next) => {
  if (req.path.startsWith('/api')) return next();
  res.sendFile(path.join(clientBuild, 'index.html'));
});

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
