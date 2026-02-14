import jwt from 'jsonwebtoken';
import config from '../config.js';

export function generateToken(peerId, roomName, displayName) {
  return jwt.sign(
    { peerId, roomName, displayName },
    config.jwtSecret,
    { expiresIn: config.jwtExpiry }
  );
}

export function verifyToken(token) {
  try {
    return jwt.verify(token, config.jwtSecret);
  } catch {
    return null;
  }
}
