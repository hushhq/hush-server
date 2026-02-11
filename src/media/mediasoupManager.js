import * as mediasoup from 'mediasoup';
import config from '../config.js';

class MediasoupManager {
  constructor() {
    this.workers = [];
    this.nextWorkerIdx = 0;
  }

  async init() {
    const { numWorkers, worker: workerSettings } = config.mediasoup;
    console.log(`[mediasoup] Starting ${numWorkers} workers...`);

    for (let i = 0; i < numWorkers; i++) {
      const worker = await mediasoup.createWorker({
        logLevel: workerSettings.logLevel,
        logTags: workerSettings.logTags,
        rtcMinPort: workerSettings.rtcMinPort,
        rtcMaxPort: workerSettings.rtcMaxPort,
      });

      worker.on('died', (error) => {
        console.error(`[mediasoup] Worker ${worker.pid} died:`, error);
        // In production: restart worker or graceful shutdown
        setTimeout(() => process.exit(1), 2000);
      });

      this.workers.push(worker);
      console.log(`[mediasoup] Worker ${worker.pid} started`);
    }
  }

  // Round-robin worker selection
  getNextWorker() {
    const worker = this.workers[this.nextWorkerIdx];
    this.nextWorkerIdx = (this.nextWorkerIdx + 1) % this.workers.length;
    return worker;
  }

  async createRouter() {
    const worker = this.getNextWorker();
    const router = await worker.createRouter({
      mediaCodecs: config.mediasoup.router.mediaCodecs,
    });
    return router;
  }

  async createWebRtcTransport(router, direction) {
    const transport = await router.createWebRtcTransport({
      ...config.mediasoup.webRtcTransport,
      appData: { direction },
    });

    // Set max incoming bitrate if configured
    if (config.mediasoup.webRtcTransport.maxIncomingBitrate) {
      try {
        await transport.setMaxIncomingBitrate(
          config.mediasoup.webRtcTransport.maxIncomingBitrate
        );
      } catch (e) {
        // Ignore — not critical
      }
    }

    return {
      transport,
      params: {
        id: transport.id,
        iceParameters: transport.iceParameters,
        iceCandidates: transport.iceCandidates,
        dtlsParameters: transport.dtlsParameters,
        sctpParameters: transport.sctpParameters,
      },
    };
  }

  close() {
    for (const worker of this.workers) {
      worker.close();
    }
    this.workers = [];
  }
}

// Singleton
const mediasoupManager = new MediasoupManager();
export default mediasoupManager;
