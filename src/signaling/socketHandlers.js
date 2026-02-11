import config from '../config.js';
import roomManager from '../rooms/roomManager.js';
import mediasoupManager from '../media/mediasoupManager.js';

export function registerSocketHandlers(io) {
  io.on('connection', (socket) => {
    const { peerId, roomName, displayName } = socket;
    console.log(`[socket] ${displayName} connected (${peerId})`);

    // Join the socket.io room for broadcasting
    socket.join(roomName);

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

        const { transport, params } = await mediasoupManager.createWebRtcTransport(room.router);

        // Store transport on peer
        peer.transports.set(transport.id, transport);

        transport.on('dtlsstatechange', (dtlsState) => {
          if (dtlsState === 'closed') {
            transport.close();
          }
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

        // Notify all other peers in the room
        socket.to(roomName).emit('newProducer', {
          producerId: producer.id,
          peerId,
          kind: producer.kind,
          appData: producer.appData,
        });

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

        // Find the receive transport
        let recvTransport = null;
        for (const t of peer.transports.values()) {
          // The second transport created is typically the recv transport
          // We identify by checking if it has no producers
          if (t.appData?.direction === 'recv' || !recvTransport) {
            recvTransport = t;
          }
        }

        if (!recvTransport) return callback({ error: 'No receive transport' });

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
          appData: consumer.appData,
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
      console.log(`[socket] ${displayName} disconnected (${peerId})`);
      roomManager.removePeer(roomName, peerId);
      socket.to(roomName).emit('peerLeft', { peerId, displayName });
    });

    // ─── Notify others that this peer joined ─────────────
    socket.to(roomName).emit('peerJoined', { peerId, displayName });
  });
}
