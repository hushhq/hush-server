/**
 * Input validation for API request body fields.
 * Rules are documented in SECURITY.md (Input validation).
 */

/** Room name (alias local part): alphanumeric, dot, underscore, hyphen, equals. Matches client joinParam. */
const ROOM_NAME_PATTERN = /^[a-zA-Z0-9._=-]+$/;
const MAX_ROOM_NAME_LENGTH = 256;

/** Participant display name: printable, no control chars. Used in LiveKit token and UI. */
const MAX_PARTICIPANT_NAME_LENGTH = 128;

/** Matrix room ID format: !opaque_id:server_name */
const MATRIX_ROOM_ID_PATTERN = /^![a-zA-Z0-9_-]+:[a-zA-Z0-9.+-]+$/;
const MAX_MATRIX_ROOM_ID_LENGTH = 255;

/** createdAt (ms): must be within last 24h to avoid abuse; future not allowed. */
const CREATED_AT_MAX_AGE_MS = 24 * 60 * 60 * 1000;

/**
 * Validates room name (LiveKit room name / alias local part).
 * @param {unknown} value
 * @returns {{ valid: true, value: string } | { valid: false, error: string }}
 */
export function validateRoomName(value) {
  if (value === undefined || value === null) {
    return { valid: false, error: 'roomName is required' };
  }
  const s = String(value).trim();
  if (s.length === 0) return { valid: false, error: 'roomName is required' };
  if (s.length > MAX_ROOM_NAME_LENGTH) {
    return { valid: false, error: `roomName must be at most ${MAX_ROOM_NAME_LENGTH} characters` };
  }
  if (!ROOM_NAME_PATTERN.test(s)) {
    return { valid: false, error: 'roomName contains invalid characters' };
  }
  return { valid: true, value: s };
}

/**
 * Validates participant display name (trimmed, length limit; no control chars).
 * @param {unknown} value
 * @returns {{ valid: true, value: string } | { valid: false, error: string }}
 */
export function validateParticipantName(value) {
  if (value === undefined || value === null || value === '') {
    return { valid: true, value: 'Participant' };
  }
  const s = String(value).trim();
  if (s.length > MAX_PARTICIPANT_NAME_LENGTH) {
    return { valid: false, error: `participantName must be at most ${MAX_PARTICIPANT_NAME_LENGTH} characters` };
  }
  if (/[\x00-\x1f\x7f]/.test(s)) {
    return { valid: false, error: 'participantName must not contain control characters' };
  }
  return { valid: true, value: s || 'Participant' };
}

/**
 * Validates Matrix room ID.
 * @param {unknown} value
 * @returns {{ valid: true, value: string } | { valid: false, error: string }}
 */
export function validateMatrixRoomId(value) {
  if (value === undefined || value === null) {
    return { valid: false, error: 'roomId is required' };
  }
  const s = String(value).trim();
  if (s.length === 0) return { valid: false, error: 'roomId is required' };
  if (s.length > MAX_MATRIX_ROOM_ID_LENGTH) {
    return { valid: false, error: `roomId must be at most ${MAX_MATRIX_ROOM_ID_LENGTH} characters` };
  }
  if (!MATRIX_ROOM_ID_PATTERN.test(s)) {
    return { valid: false, error: 'roomId must be a valid Matrix room ID' };
  }
  return { valid: true, value: s };
}

/**
 * Validates createdAt timestamp (ms). Must be a number within the last CREATED_AT_MAX_AGE_MS.
 * @param {unknown} value
 * @returns {{ valid: true, value: number } | { valid: false, error: string }}
 */
export function validateCreatedAt(value) {
  if (value === undefined || value === null) {
    return { valid: false, error: 'createdAt is required' };
  }
  const n = Number(value);
  if (!Number.isFinite(n)) return { valid: false, error: 'createdAt must be a number' };
  const now = Date.now();
  if (n > now) return { valid: false, error: 'createdAt must not be in the future' };
  if (now - n > CREATED_AT_MAX_AGE_MS) {
    return { valid: false, error: 'createdAt is too old' };
  }
  return { valid: true, value: n };
}
