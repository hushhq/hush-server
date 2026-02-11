import os from 'os';
import config from '../config.js';

/**
 * ResourcePool — Manages server capacity allocation between tiers.
 *
 * This is NOT an artificial paywall. Resources are physically allocated
 * to each pool based on actual server capacity. When the free pool is
 * full, it means the allocated resources for free rooms are genuinely
 * exhausted — not that the server is "pretending" to be full.
 *
 * Admins configure pool sizes via environment variables.
 * Self-hosters can set FREE_POOL_PERCENT=100 to disable tiers entirely.
 */
class ResourcePool {
  constructor() {
    // Pool allocation percentages (must sum to <= 100)
    this.freePoolPercent = parseInt(process.env.FREE_POOL_PERCENT || '60');
    this.supporterPoolPercent = parseInt(process.env.SUPPORTER_POOL_PERCENT || '30');
    this.reservePercent = 100 - this.freePoolPercent - this.supporterPoolPercent;

    // Calculate actual room limits from total capacity
    const totalMaxRooms = parseInt(process.env.TOTAL_MAX_ROOMS || '50');

    this.maxFreeRooms = Math.floor(totalMaxRooms * (this.freePoolPercent / 100));
    this.maxSupporterRooms = Math.floor(totalMaxRooms * (this.supporterPoolPercent / 100));

    // Live counters
    this.activeFreeRooms = 0;
    this.activeSupporterRooms = 0;

    console.log(`[resources] Pool allocation:`);
    console.log(`  Free:      ${this.freePoolPercent}% → max ${this.maxFreeRooms} rooms`);
    console.log(`  Supporter: ${this.supporterPoolPercent}% → max ${this.maxSupporterRooms} rooms`);
    console.log(`  Reserve:   ${this.reservePercent}%`);
  }

  /**
   * Check if a tier has capacity for a new room.
   * Returns { allowed, reason, pool }
   */
  canCreateRoom(tier = 'free') {
    if (tier === 'supporter') {
      if (this.activeSupporterRooms >= this.maxSupporterRooms) {
        return {
          allowed: false,
          reason: 'supporter_pool_full',
          pool: this.getSupporterPoolStatus(),
        };
      }
      return { allowed: true, pool: this.getSupporterPoolStatus() };
    }

    // Free tier
    if (this.activeFreeRooms >= this.maxFreeRooms) {
      return {
        allowed: false,
        reason: 'free_pool_full',
        pool: this.getFreePoolStatus(),
      };
    }
    return { allowed: true, pool: this.getFreePoolStatus() };
  }

  /**
   * Register a new active room in its pool
   */
  addRoom(tier = 'free') {
    if (tier === 'supporter') {
      this.activeSupporterRooms++;
    } else {
      this.activeFreeRooms++;
    }
  }

  /**
   * Release a room slot back to its pool
   */
  removeRoom(tier = 'free') {
    if (tier === 'supporter') {
      this.activeSupporterRooms = Math.max(0, this.activeSupporterRooms - 1);
    } else {
      this.activeFreeRooms = Math.max(0, this.activeFreeRooms - 1);
    }
  }

  // ─── Status getters (public, no sensitive data) ─────

  getFreePoolStatus() {
    return {
      active: this.activeFreeRooms,
      max: this.maxFreeRooms,
      available: this.maxFreeRooms - this.activeFreeRooms,
    };
  }

  getSupporterPoolStatus() {
    return {
      active: this.activeSupporterRooms,
      max: this.maxSupporterRooms,
      available: this.maxSupporterRooms - this.activeSupporterRooms,
    };
  }

  /**
   * Full server status — exposed via /api/status
   * All data here is public and transparent by design.
   */
  getPublicStatus() {
    const totalActive = this.activeFreeRooms + this.activeSupporterRooms;
    const totalMax = this.maxFreeRooms + this.maxSupporterRooms;

    return {
      free: this.getFreePoolStatus(),
      supporter: this.getSupporterPoolStatus(),
      total: {
        active: totalActive,
        capacity: totalMax,
        utilizationPercent: totalMax > 0
          ? Math.round((totalActive / totalMax) * 100)
          : 0,
      },
      allocation: {
        freePercent: this.freePoolPercent,
        supporterPercent: this.supporterPoolPercent,
        reservePercent: this.reservePercent,
      },
    };
  }

  /**
   * Get system resource info (CPU, memory)
   * Useful for self-hosters to monitor their instance
   */
  getSystemInfo() {
    const totalMem = os.totalmem();
    const freeMem = os.freemem();
    const loadAvg = os.loadavg();

    return {
      memory: {
        totalMB: Math.round(totalMem / 1024 / 1024),
        freeMB: Math.round(freeMem / 1024 / 1024),
        usedPercent: Math.round(((totalMem - freeMem) / totalMem) * 100),
      },
      cpu: {
        cores: os.cpus().length,
        loadAvg1m: loadAvg[0].toFixed(2),
        loadAvg5m: loadAvg[1].toFixed(2),
      },
      uptime: Math.round(process.uptime()),
    };
  }
}

const resourcePool = new ResourcePool();
export default resourcePool;
