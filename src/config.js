import 'dotenv/config';

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
      maxParticipants: parseInt(process.env.FREE_MAX_PARTICIPANTS || '8'),
      maxScreenShares: parseInt(process.env.FREE_MAX_SCREEN_SHARES || '8'),
      maxBitrate: parseInt(process.env.FREE_MAX_BITRATE || '12000000'),
      maxQuality: 'source',
    },
    supporter: {
      maxParticipants: parseInt(process.env.SUPPORTER_MAX_PARTICIPANTS || '8'),
      maxScreenShares: parseInt(process.env.SUPPORTER_MAX_SCREEN_SHARES || '8'),
      maxBitrate: parseInt(process.env.SUPPORTER_MAX_BITRATE || '12000000'),
      maxQuality: 'source',
    },
  },

  // CORS
  corsOrigin: process.env.CORS_ORIGIN || 'http://localhost:5173',
};

export default config;
