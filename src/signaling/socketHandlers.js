import config from '../config.js';
import roomManager from '../rooms/roomManager.js';
import mediasoupManager from '../media/mediasoupManager.js';

export function registerSocketHandlers(io) {
  io.on('connection', (socket) => {
    const { peerId, roomName, displayName } = socket;
    console.log(`[socket] ${displayName} connected (${peerId}, socket: ${socket.id})`);

    // Join the socket.io room for broadcasting
    socket.join(roomName);

    // ─── Ensure peer exists in room (handles reconnect after disconnect) ───
    const room = roomManager.getRoom(roomName);
    if (room) {
      const existingPeer = room.peers.get(peerId);
      if (existingPeer) {
        // Peer already exists — reconnection. Reset stale mediasoup state.
        roomManager.resetPeer(roomName, peerId, socket.id);
      } else {
        // Peer was removed (disconnect fired before reconnect). Re-add.
        try {
          roomManager.addPeer(roomName, peerId, displayName, socket.id);
        } catch (err) {
          console.error(`[socket] Failed to re-register peer: ${err.message}`);
          socket.emit('error', { message: err.message });
          socket.disconnect(true);
          return;
        }
      }
    }

    // ─── Get Router RTP Capabilities ─────────────────────
    // Client needs these to initialize mediasoup-client Device
    socket.on('getRouterRtpCapabilities', (_, callback) => {
      try {
        const room = roomManager.getRoom(roomName);
        if (!room) return callback({ error: 'Room not found' });
        callback({ rtpCapabilities: room.router.rtpCapabilities });
      } catch (err) {
        callback({ error: err.message });
      }
    });

    // ─── Create Transport ────────────────────────────────
    // Each peer needs a send transport and a receive transport
    socket.on('createWebRtcTransport', async ({ direction }, callback) => {
      try {
        const room = roomManager.getRoom(roomName);
        if (!room) return callback({ error: 'Room not found' });

        const peer = room.peers.get(peerId);
        if (!peer) return callback({ error: 'Peer not found' });

        const { transport, params } = await mediasoupManager.createWebRtcTransport(room.router, direction);

        // Store transport on peer
        peer.transports.set(transport.id, transport);

        transport.on('dtlsstatechange', (dtlsState) => {
          console.log(`[transport] ${peerId} ${direction} DTLS: ${dtlsState}`);
          if (dtlsState === 'closed') {
            transport.close();
          }
        });

        transport.on('icestatechange', (iceState) => {
          console.log(`[transport] ${peerId} ${direction} ICE: ${iceState}`);
        });

        transport.on('close', () => {
          peer.transports.delete(transport.id);
        });

        callback({ params });
      } catch (err) {
        callback({ error: err.message });
      }
    });

    // ─── Connect Transport ───────────────────────────────
    socket.on('connectTransport', async ({ transportId, dtlsParameters }, callback) => {
      try {
        const room = roomManager.getRoom(roomName);
        const peer = room?.peers.get(peerId);
        const transport = peer?.transports.get(transportId);
        if (!transport) return callback({ error: 'Transport not found' });

        await transport.connect({ dtlsParameters });
        callback({});
      } catch (err) {
        callback({ error: err.message });
      }
    });

    // ─── Produce (start sending media) ───────────────────
    socket.on('produce', async ({ transportId, kind, rtpParameters, appData }, callback) => {
      try {
        const room = roomManager.getRoom(roomName);
        const peer = room?.peers.get(peerId);
        const transport = peer?.transports.get(transportId);
        if (!transport) return callback({ error: 'Transport not found' });

        // Enforce screen share limit (room-specific based on tier)
        if (appData?.source === 'screen') {
          const activeShares = roomManager.getActiveScreenShares(roomName);
          const maxShares = room.limits?.maxScreenShares || config.maxScreenSharesPerRoom;
          if (activeShares >= maxShares) {
            return callback({ error: `Max ${maxShares} screen shares allowed` });
          }
        }

        const producer = await transport.produce({
          kind,
          rtpParameters,
          appData,
        });

        peer.producers.set(producer.id, producer);

        producer.on('transportclose', () => {
          peer.producers.delete(producer.id);
        });

        // Notify all other peers (skip internal warmup producers)
        if (appData?.source !== '_warmup') {
          socket.to(roomName).emit('newProducer', {
            producerId: producer.id,
            peerId,
            kind: producer.kind,
            appData: producer.appData,
          });
        }

        callback({ producerId: producer.id });
      } catch (err) {
        callback({ error: err.message });
      }
    });

    // ─── Consume (start receiving media from a producer) ─
    socket.on('consume', async ({ producerId, rtpCapabilities }, callback) => {
      try {
        const room = roomManager.getRoom(roomName);
        if (!room) return callback({ error: 'Room not found' });

        // Check if the router can consume this producer
        if (!room.router.canConsume({ producerId, rtpCapabilities })) {
          return callback({ error: 'Cannot consume' });
        }

        const peer = room.peers.get(peerId);
        if (!peer) return callback({ error: 'Peer not found' });

        // Find the receive transport by direction stored in appData
        const recvTransport = Array.from(peer.transports.values())
          .find((t) => t.appData?.direction === 'recv');

        if (!recvTransport) return callback({ error: 'No receive transport' });

        // Look up the producer to relay its appData (source type, etc.)
        let producerAppData = {};
        for (const p of room.peers.values()) {
          const prod = p.producers.get(producerId);
          if (prod) {
            producerAppData = prod.appData;
            break;
          }
        }

        const consumer = await recvTransport.consume({
          producerId,
          rtpCapabilities,
          paused: true, // Start paused, client resumes after setup
        });

        peer.consumers.set(consumer.id, consumer);

        consumer.on('transportclose', () => {
          peer.consumers.delete(consumer.id);
        });

        consumer.on('producerclose', () => {
          peer.consumers.delete(consumer.id);
          socket.emit('consumerClosed', { consumerId: consumer.id });
        });

        callback({
          consumerId: consumer.id,
          producerId,
          kind: consumer.kind,
          rtpParameters: consumer.rtpParameters,
          appData: producerAppData,
        });
      } catch (err) {
        callback({ error: err.message });
      }
    });

    // ─── Resume Consumer ─────────────────────────────────
    socket.on('resumeConsumer', async ({ consumerId }, callback) => {
      try {
        const room = roomManager.getRoom(roomName);
        const peer = room?.peers.get(peerId);
        const consumer = peer?.consumers.get(consumerId);
        if (!consumer) return callback({ error: 'Consumer not found' });

        await consumer.resume();
        console.log(`[mediasoup] Consumer ${consumerId} resumed (peer: ${peerId})`);
        callback({});
      } catch (err) {
        callback({ error: err.message });
      }
    });

    // ─── Close Producer (stop sharing) ───────────────────
    socket.on('closeProducer', async ({ producerId }, callback) => {
      try {
        const room = roomManager.getRoom(roomName);
        const peer = room?.peers.get(peerId);
        const producer = peer?.producers.get(producerId);
        if (!producer) return callback?.({ error: 'Producer not found' });

        producer.close();
        peer.producers.delete(producerId);

        // Notify others
        socket.to(roomName).emit('producerClosed', { producerId, peerId });
        callback?.({});
      } catch (err) {
        callback?.({ error: err.message });
      }
    });

    // ─── Replace Track (switch screen/window on the fly) ─
    // Client-side operation — server just needs to know about appData updates
    socket.on('updateProducerAppData', ({ producerId, appData }, callback) => {
      try {
        const room = roomManager.getRoom(roomName);
        const peer = room?.peers.get(peerId);
        const producer = peer?.producers.get(producerId);
        if (!producer) return callback?.({ error: 'Producer not found' });

        // Update appData (e.g., when switching from screen A to screen B)
        Object.assign(producer.appData, appData);

        // Notify others about the change
        socket.to(roomName).emit('producerUpdated', {
          producerId,
          peerId,
          appData: producer.appData,
        });

        callback?.({});
      } catch (err) {
        callback?.({ error: err.message });
      }
    });

    // ─── Get Peers (for joining existing room) ───────────
    socket.on('getPeers', (_, callback) => {
      try {
        const peers = roomManager.getPeers(roomName);
        callback({ peers: peers.filter((p) => p.id !== peerId) });
      } catch (err) {
        callback({ error: err.message });
      }
    });

    // ─── Chat Message ────────────────────────────────────
    socket.on('sendMessage', ({ text }, callback) => {
      try {
        // Validate input
        if (!text || typeof text !== 'string') {
          return callback({ error: 'Message text required' });
        }

        const trimmedText = text.trim();

        if (trimmedText.length === 0) {
          return callback({ error: 'Message cannot be empty' });
        }
        if (trimmedText.length > 2000) {
          return callback({ error: 'Message too long (max 2000 characters)' });
        }

        // Construct and broadcast message
        const message = {
          messageId: `msg_${peerId}_${Date.now()}`,
          peerId,
          displayName,
          text: trimmedText,
          timestamp: Date.now(),
        };

        // Broadcast to all peers in room (including sender)
        io.in(roomName).emit('messageReceived', message);

        callback({});
      } catch (err) {
        callback({ error: err.message });
      }
    });

    // ─── E2E Key Exchange ────────────────────────────────
    // The server relays the encrypted key material without reading it
    socket.on('e2eKeyExchange', ({ targetPeerId, keyMaterial }) => {
      // Find the target peer's socket and send the key
      const targetSocket = Array.from(io.sockets.sockets.values())
        .find((s) => s.peerId === targetPeerId && s.roomName === roomName);

      if (targetSocket) {
        targetSocket.emit('e2eKeyExchange', {
          fromPeerId: peerId,
          keyMaterial,
        });
      }
    });

    // ─── Disconnect ──────────────────────────────────────
    socket.on('disconnect', () => {
      console.log(`[socket] ${displayName} disconnected (${peerId}, socket: ${socket.id})`);

      // Only remove peer if this socket is still the active one.
      // On page reload, a new socket may have already re-registered the peer.
      const currentRoom = roomManager.getRoom(roomName);
      const currentPeer = currentRoom?.peers.get(peerId);
      if (currentPeer && currentPeer.socketId === socket.id) {
        roomManager.removePeer(roomName, peerId);
        socket.to(roomName).emit('peerLeft', { peerId, displayName });
      }
    });

    // ─── Notify others that this peer joined ─────────────
    socket.to(roomName).emit('peerJoined', { peerId, displayName });
  });
}
