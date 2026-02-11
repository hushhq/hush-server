import bcrypt from 'bcrypt';
import config from '../config.js';
import mediasoupManager from '../media/mediasoupManager.js';
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
    const router = await mediasoupManager.createRouter();
    const tierConfig = config.tiers[tier] || config.tiers.free;

    const room = {
      name: roomName,
      passwordHash: hashedPassword,
      creatorId,
      tier,
      router,
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
      e2eEnabled: false,
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
      // mediasoup transports and producers/consumers
      transports: new Map(),  // Map<transportId, Transport>
      producers: new Map(),   // Map<producerId, Producer>
      consumers: new Map(),   // Map<consumerId, Consumer>
    };

    room.peers.set(peerId, peer);
    console.log(`[room] "${roomName}": ${displayName} joined (${room.peers.size}/${config.maxParticipantsPerRoom})`);
    return peer;
  }

  /**
   * Reset a peer's mediasoup state on reconnection (new socket for same peerId).
   * Closes old transports and clears producers/consumers, then updates socketId.
   */
  resetPeer(roomName, peerId, newSocketId) {
    const room = this.rooms.get(roomName);
    if (!room) return null;

    const peer = room.peers.get(peerId);
    if (!peer) return null;

    // Close stale transports (also closes their producers/consumers)
    for (const transport of peer.transports.values()) {
      transport.close();
    }
    peer.transports.clear();
    peer.producers.clear();
    peer.consumers.clear();
    peer.socketId = newSocketId;

    console.log(`[room] "${roomName}": ${peer.displayName} reconnected (socket: ${newSocketId})`);
    return peer;
  }

  removePeer(roomName, peerId) {
    const room = this.rooms.get(roomName);
    if (!room) return;

    const peer = room.peers.get(peerId);
    if (!peer) return;

    // Close all mediasoup transports (this also closes producers/consumers)
    for (const transport of peer.transports.values()) {
      transport.close();
    }

    room.peers.delete(peerId);
    console.log(`[room] "${roomName}": ${peer.displayName} left (${room.peers.size}/${config.maxParticipantsPerRoom})`);

    // Auto-delete empty rooms
    if (room.peers.size === 0) {
      room.router.close();
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
      producers: Array.from(peer.producers.values()).map((p) => ({
        id: p.id,
        kind: p.kind,
        appData: p.appData,
      })),
    }));
  }

  getActiveScreenShares(roomName) {
    const room = this.rooms.get(roomName);
    if (!room) return 0;
    let count = 0;
    for (const peer of room.peers.values()) {
      for (const producer of peer.producers.values()) {
        if (producer.appData?.source === 'screen') count++;
      }
    }
    return count;
  }
}

const roomManager = new RoomManager();
export default roomManager;
