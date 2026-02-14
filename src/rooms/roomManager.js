import bcrypt from 'bcrypt';
import config from '../config.js';
import resourcePool from './resourcePool.js';

class RoomManager {
  constructor() {
    // Map<roomId, Room>
    this.rooms = new Map();
  }

  async createRoom(roomName, password, creatorId, tier = 'free') {
    if (this.rooms.has(roomName)) {
      throw new Error('Room already exists');
    }

    // Check resource pool capacity
    const { allowed, reason } = resourcePool.canCreateRoom(tier);
    if (!allowed) {
      const err = new Error(
        reason === 'free_pool_full'
          ? 'FREE_POOL_FULL'
          : 'SUPPORTER_POOL_FULL'
      );
      err.code = reason;
      err.pool = resourcePool.getPublicStatus();
      throw err;
    }

    const hashedPassword = await bcrypt.hash(password, config.bcryptRounds);
    const tierConfig = config.tiers[tier] || config.tiers.free;

    const room = {
      name: roomName,
      passwordHash: hashedPassword,
      creatorId,
      tier,
      // Tier-specific limits applied to this room
      limits: {
        maxParticipants: tierConfig.maxParticipants,
        maxScreenShares: tierConfig.maxScreenShares,
        maxBitrate: tierConfig.maxBitrate,
        maxQuality: tierConfig.maxQuality,
      },
      // Map<peerId, Peer>
      peers: new Map(),
      createdAt: Date.now(),
    };

    this.rooms.set(roomName, room);
    resourcePool.addRoom(tier);
    console.log(`[room] Created: "${roomName}" [${tier}] by ${creatorId}`);
    return room;
  }

  async verifyPassword(roomName, password) {
    const room = this.rooms.get(roomName);
    if (!room) return false;
    return bcrypt.compare(password, room.passwordHash);
  }

  getRoom(roomName) {
    return this.rooms.get(roomName);
  }

  getRoomList() {
    // Return room names and participant counts only — no sensitive data
    return Array.from(this.rooms.values()).map((room) => ({
      name: room.name,
      tier: room.tier,
      participants: room.peers.size,
      maxParticipants: room.limits.maxParticipants,
      createdAt: room.createdAt,
    }));
  }

  addPeer(roomName, peerId, displayName, socketId = null) {
    const room = this.rooms.get(roomName);
    if (!room) throw new Error('Room not found');
    if (room.peers.size >= room.limits.maxParticipants) {
      throw new Error('Room is full');
    }

    const peer = {
      id: peerId,
      displayName,
      socketId,
      joinedAt: Date.now(),
    };

    room.peers.set(peerId, peer);
    console.log(`[room] "${roomName}": ${displayName} joined (${room.peers.size}/${config.maxParticipantsPerRoom})`);
    return peer;
  }

  /**
   * Reset a peer's state on reconnection (new socket for same peerId).
   * Updates socketId for the peer.
   */
  resetPeer(roomName, peerId, newSocketId) {
    const room = this.rooms.get(roomName);
    if (!room) return null;

    const peer = room.peers.get(peerId);
    if (!peer) return null;

    peer.socketId = newSocketId;

    console.log(`[room] "${roomName}": ${peer.displayName} reconnected (socket: ${newSocketId})`);
    return peer;
  }

  removePeer(roomName, peerId) {
    const room = this.rooms.get(roomName);
    if (!room) return;

    const peer = room.peers.get(peerId);
    if (!peer) return;

    room.peers.delete(peerId);
    console.log(`[room] "${roomName}": ${peer.displayName} left (${room.peers.size}/${config.maxParticipantsPerRoom})`);

    // Auto-delete empty rooms
    if (room.peers.size === 0) {
      resourcePool.removeRoom(room.tier);
      this.rooms.delete(roomName);
      console.log(`[room] "${roomName}" [${room.tier}] deleted (empty)`);
    }
  }

  getPeers(roomName) {
    const room = this.rooms.get(roomName);
    if (!room) return [];
    return Array.from(room.peers.values()).map((peer) => ({
      id: peer.id,
      displayName: peer.displayName,
    }));
  }

  getActiveScreenShares(roomName) {
    const room = this.rooms.get(roomName);
    if (!room) return 0;
    // Screen shares are now tracked by LiveKit, not in roomManager
    // This method is preserved for API compatibility but returns 0
    return 0;
  }
}

const roomManager = new RoomManager();
export default roomManager;
