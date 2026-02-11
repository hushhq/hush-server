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

// Socket.io middleware to verify JWT on connection
export function socketAuthMiddleware(socket, next) {
  const token = socket.handshake.auth?.token;
  if (!token) {
    return next(new Error('Authentication required'));
  }

  const payload = verifyToken(token);
  if (!payload) {
    return next(new Error('Invalid or expired token'));
  }

  // Attach user info to socket
  socket.peerId = payload.peerId;
  socket.roomName = payload.roomName;
  socket.displayName = payload.displayName;
  next();
}
