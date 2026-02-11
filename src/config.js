import 'dotenv/config';
import os from 'os';

// Detect number of CPU cores for mediasoup workers
const numWorkers = Math.min(os.cpus().length, 4);

const config = {
  // Server
  port: parseInt(process.env.PORT || '3001'),
  host: process.env.HOST || '0.0.0.0',

  // Auth
  jwtSecret: process.env.JWT_SECRET || 'CHANGE_ME_IN_PRODUCTION_' + Math.random().toString(36),
  jwtExpiry: '24h',
  bcryptRounds: 12,

  // Room limits (defaults — overridden per tier)
  maxParticipantsPerRoom: parseInt(process.env.MAX_PARTICIPANTS || '10'),
  maxScreenSharesPerRoom: parseInt(process.env.MAX_SCREEN_SHARES || '3'),

  // Tier-specific limits
  tiers: {
    free: {
      maxParticipants: parseInt(process.env.FREE_MAX_PARTICIPANTS || '4'),
      maxScreenShares: parseInt(process.env.FREE_MAX_SCREEN_SHARES || '1'),
      maxBitrate: parseInt(process.env.FREE_MAX_BITRATE || '4500000'), // 1080p
      maxQuality: '1080p',
    },
    supporter: {
      maxParticipants: parseInt(process.env.SUPPORTER_MAX_PARTICIPANTS || '10'),
      maxScreenShares: parseInt(process.env.SUPPORTER_MAX_SCREEN_SHARES || '3'),
      maxBitrate: parseInt(process.env.SUPPORTER_MAX_BITRATE || '15000000'), // 4K
      maxQuality: '4K',
    },
  },

  // CORS
  corsOrigin: process.env.CORS_ORIGIN || 'http://localhost:5173',

  // mediasoup
  mediasoup: {
    numWorkers,

    worker: {
      rtcMinPort: parseInt(process.env.RTC_MIN_PORT || '40000'),
      rtcMaxPort: parseInt(process.env.RTC_MAX_PORT || '40100'),
      logLevel: 'warn',
      logTags: ['info', 'ice', 'dtls', 'rtp', 'srtp', 'rtcp'],
    },

    router: {
      mediaCodecs: [
        {
          kind: 'audio',
          mimeType: 'audio/opus',
          clockRate: 48000,
          channels: 2,
        },
        // H.264 first — mandatory for Safari/iOS compatibility.
        // mediasoup prefers the first codec; all browsers support H.264.
        {
          kind: 'video',
          mimeType: 'video/H264',
          clockRate: 90000,
          parameters: {
            'packetization-mode': 1,
            'profile-level-id': '4d0032',
            'level-asymmetry-allowed': 1,
            'x-google-start-bitrate': 1000,
          },
        },
        {
          kind: 'video',
          mimeType: 'video/VP8',
          clockRate: 90000,
          parameters: {
            'x-google-start-bitrate': 1000,
          },
        },
        {
          kind: 'video',
          mimeType: 'video/VP9',
          clockRate: 90000,
          parameters: {
            'profile-id': 2,
            'x-google-start-bitrate': 1000,
          },
        },
      ],
    },

    // WebRTC transport settings
    webRtcTransport: {
      listenInfos: [
        {
          protocol: 'udp',
          ip: '0.0.0.0',
          announcedAddress: process.env.ANNOUNCED_IP || null,
        },
        {
          protocol: 'tcp',
          ip: '0.0.0.0',
          announcedAddress: process.env.ANNOUNCED_IP || null,
        },
      ],
      initialAvailableOutgoingBitrate: 1000000,
      minimumAvailableOutgoingBitrate: 600000,
      maxSctpMessageSize: 262144,
      maxIncomingBitrate: 15000000, // 15 Mbps — generous for 1080p+
    },
  },
};

export default config;
