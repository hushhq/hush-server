# E2EE Testing Checklist

This document provides a comprehensive manual testing checklist for verifying End-to-End Encryption (E2EE) functionality in the Hush app. The application implements E2EE at two layers:

1. **Matrix E2EE**: Chat messages encrypted using Matrix's Olm/Megolm protocol
2. **LiveKit E2EE**: WebRTC media streams (screen share, audio) encrypted with LiveKit's E2EE framework

## Prerequisites

- Docker services running (`docker-compose up -d`)
- Client dev server running (`npm run dev --prefix client`)
- Two browser windows/tabs for multi-user testing (recommend: normal + incognito)
- Browser DevTools open (F12) with Console tab visible

## Test Environment Setup

### Browser 1 (Normal Mode)
1. Navigate to `http://localhost:5173`
2. Open DevTools (F12) → Console tab
3. Clear console before each test

### Browser 2 (Incognito Mode)
1. Open incognito/private window
2. Navigate to `http://localhost:5173`
3. Open DevTools (F12) → Console tab
4. Clear console before each test

---

## Part 1: Matrix E2EE Chat Testing

### Test 1.1: Crypto Initialization on Guest Login

**Objective**: Verify Matrix crypto module initializes when user logs in as guest.

**Steps**:
1. Browser 1: Click "Continue as Guest"
2. Watch console output

**Expected Console Output**:
```
[matrix] loginAsGuest: start
[matrix] loginAsGuest: registration response received
[matrix] initCrypto: starting crypto initialization
[matrix] initCrypto: crypto initialized successfully
[matrix] loginAsGuest: startClient called
[matrix] loginAsGuest: sync completed
[matrix] loginAsGuest: complete
```

**Verification**:
- ✅ No crypto-related errors in console
- ✅ `initCrypto: crypto initialized successfully` message appears
- ✅ User reaches home screen with "Create Room" button

**Common Issues**:
- If crypto init fails, check IndexedDB storage is enabled in browser settings
- Safari private mode may block IndexedDB - use normal mode for testing

---

### Test 1.2: IndexedDB Crypto Store Creation

**Objective**: Verify Matrix creates persistent crypto store in IndexedDB.

**Steps**:
1. Browser 1: Login as guest (if not already logged in)
2. DevTools → Application tab → IndexedDB
3. Expand IndexedDB tree

**Expected Result**:
- ✅ Database named `hush-crypto` exists
- ✅ Object stores present:
  - `account`
  - `sessions`
  - `inbound_group_sessions`
  - `device_data`
  - `rooms`

**Verification via Console**:
```javascript
// Paste in console to check crypto store
indexedDB.databases().then(dbs => console.log(dbs))
```

**Expected Output**:
```javascript
[
  { name: "hush-crypto", version: 11 },
  { name: "matrix-js-sdk:default", version: ... }
]
```

**Common Issues**:
- If `hush-crypto` database missing, check browser console for crypto initialization errors
- Verify IndexedDB is not disabled in browser settings

---

### Test 1.3: Room Creation with Crypto Status Check

**Objective**: Verify crypto readiness is logged before room creation.

**Steps**:
1. Browser 1: Enter room name "test-e2ee-chat"
2. Click "Create Room"
3. Watch console output

**Expected Console Output**:
```
[home] Crypto status before room operations: {
  cryptoEnabled: true,
  deviceId: "ABCDEFGHIJ",
  userId: "@guest_abc123:hush-local.com"
}
[home] createRoom response: { room_id: "!xyz:hush-local.com" }
```

**Verification**:
- ✅ `cryptoEnabled: true` in console output
- ✅ `deviceId` is a 10-character string
- ✅ `userId` matches the guest user ID format
- ✅ Room creation succeeds with room_id in response

**Common Issues**:
- If `cryptoEnabled: false`, check that crypto initialization completed before room creation
- If room creation fails with alias collision, verify unique room names are being used

---

### Test 1.4: Chat Component Crypto Status on Mount

**Objective**: Verify Chat component logs crypto status when mounting.

**Steps**:
1. Browser 1: After creating room "test-e2ee-chat", observe Chat sidebar
2. Watch console output when Chat component mounts

**Expected Console Output**:
```
[chat] Component mounted with crypto status: {
  cryptoEnabled: true,
  roomId: "!xyz:hush-local.com",
  deviceId: "ABCDEFGHIJ",
  userId: "@guest_abc123:hush-local.com"
}
```

**Verification**:
- ✅ `cryptoEnabled: true` in console output
- ✅ `roomId` matches the created room ID
- ✅ No errors about missing crypto module

**Common Issues**:
- If Chat doesn't mount, check browser console for React errors
- If `cryptoEnabled: false`, crypto may not have initialized before startClient()

---

### Test 1.5: Send Encrypted Message (Auto-Encryption)

**Objective**: Verify Matrix SDK automatically encrypts messages in encrypted rooms.

**Steps**:
1. Browser 1: In room chat, type "Hello E2EE world"
2. Click Send or press Enter
3. Watch console output

**Expected Console Output**:
```
[chat] Timeline event received: {
  type: "m.room.message",
  isEncrypted: false,
  isMessage: true,
  sender: "@guest_abc123:hush-local.com",
  eventId: "$event123"
}
```

**Expected UI**:
- ✅ Message appears in chat list immediately
- ✅ Message shows sender's user ID
- ✅ Input field clears after sending

**Network Verification**:
1. DevTools → Network tab
2. Filter for `send` or `message`
3. Find `/_matrix/client/v3/rooms/.../send/m.room.message/...`
4. Inspect request payload

**Expected Network Payload** (encrypted):
```json
{
  "algorithm": "m.megolm.v1.aes-sha2",
  "ciphertext": "AwgAEn...",
  "device_id": "ABCDEFGHIJ",
  "sender_key": "...",
  "session_id": "..."
}
```

**Note**: If encryption is working, the payload will contain `algorithm` and `ciphertext` fields instead of plain `body` text.

**Common Issues**:
- If message doesn't send, check console for `room configured for encryption but client does not support` error
- This error indicates crypto wasn't initialized - verify Test 1.1 passes

---

### Test 1.6: Receive and Decrypt Message (Multi-User)

**Objective**: Verify second user can decrypt messages sent by first user.

**Steps**:
1. Browser 2 (Incognito): Login as guest
2. Browser 2: Join room "test-e2ee-chat"
3. Watch console for crypto status and timeline events
4. Browser 1: Send message "Testing cross-user E2EE"
5. Browser 2: Observe console and UI

**Expected Console Output (Browser 2)**:
```
[home] Crypto status before room operations: {
  cryptoEnabled: true,
  deviceId: "KLMNOPQRST",
  userId: "@guest_xyz789:hush-local.com"
}
[chat] Component mounted with crypto status: {
  cryptoEnabled: true,
  roomId: "!xyz:hush-local.com",
  deviceId: "KLMNOPQRST",
  userId: "@guest_xyz789:hush-local.com"
}
[chat] Timeline event received: {
  type: "m.room.encrypted",
  isEncrypted: true,
  isMessage: false,
  sender: "@guest_abc123:hush-local.com",
  eventId: "$encrypted_event"
}
[chat] Timeline event received: {
  type: "m.room.message",
  isEncrypted: false,
  isMessage: true,
  sender: "@guest_abc123:hush-local.com",
  eventId: "$decrypted_event"
}
```

**Expected UI (Browser 2)**:
- ✅ Message "Testing cross-user E2EE" appears in chat
- ✅ Message shows sender's user ID from Browser 1
- ✅ No "Unable to decrypt" placeholder

**Verification**:
- ✅ Console shows both `m.room.encrypted` (received) and `m.room.message` (decrypted) events
- ✅ Two separate timeline events logged for same message (encrypted wire format → decrypted display)
- ✅ Message content readable in UI

**Common Issues**:
- If message shows "Unable to decrypt", check both browsers initialized crypto successfully
- Verify Browser 2's crypto store has received encryption keys via to-device messages
- Check Network tab for `/_matrix/client/v3/sendToDevice/m.room_key/...` requests (key sharing)

---

### Test 1.7: Message Persistence After Page Reload

**Objective**: Verify encrypted messages persist and decrypt after page reload.

**Steps**:
1. Browser 1: Verify at least 2 messages sent in room "test-e2ee-chat"
2. Browser 1: Reload page (F5 or Cmd+R)
3. Browser 1: Login as guest again
4. Browser 1: Join room "test-e2ee-chat"
5. Observe Chat sidebar

**Expected Result**:
- ✅ All previously sent messages appear in chat
- ✅ Messages are decrypted and readable
- ✅ No "Unable to decrypt" errors

**Expected Console Output**:
```
[chat] Component mounted with crypto status: {
  cryptoEnabled: true,
  roomId: "!xyz:hush-local.com",
  deviceId: "ABCDEFGHIJ",
  userId: "@guest_abc123:hush-local.com"
}
```

**Verification**:
- ✅ Crypto module re-initialized from IndexedDB store
- ✅ Device ID remains same after reload (keys persisted)
- ✅ Room timeline loads with decrypted messages

**Common Issues**:
- If messages show "Unable to decrypt" after reload, crypto store may not have persisted keys
- Check IndexedDB `hush-crypto` database still exists after reload
- Verify `inbound_group_sessions` object store contains session data

---

## Part 2: LiveKit E2EE Media Testing

### Test 2.1: E2EE Key Generation for Room Creator

**Objective**: Verify room creator generates random E2EE key for LiveKit.

**Steps**:
1. Browser 1: Create room "test-e2ee-media"
2. Browser 1: Click screen share button to start screen sharing
3. Watch console output

**Expected Console Output**:
```
[livekit] Generated new random E2EE key (room creator)
[livekit] E2EE key provider and worker initialized
[livekit] E2EE enabled with Matrix key distribution
[livekit] room.e2eeManager: E2EEManager { ... }
```

**Verification**:
- ✅ "Generated new random E2EE key (room creator)" message appears
- ✅ "E2EE key provider and worker initialized" appears
- ✅ "E2EE enabled with Matrix key distribution" appears
- ✅ `room.e2eeManager` is not null/undefined
- ✅ Screen share starts successfully

**SessionStorage Verification**:
```javascript
// Paste in console to check E2EE key storage
console.log('E2EE key stored:', sessionStorage.getItem('livekit-e2ee-key-test-e2ee-media'))
```

**Expected Output**:
```
E2EE key stored: abc123def456... (base64 string)
```

**Common Issues**:
- If E2EE key generation fails, check browser console for worker initialization errors
- Verify `livekit-client/e2ee-worker` is accessible (Vite should bundle it)
- Check for HTTPS/localhost requirement (E2EE worker requires secure context)

---

### Test 2.2: E2EE Key Distribution to Late Joiner

**Objective**: Verify room creator sends E2EE key to participants via Matrix.

**Steps**:
1. Browser 1: In room "test-e2ee-media", ensure screen share is active
2. Browser 2 (Incognito): Login as guest and join "test-e2ee-media"
3. Watch console output in BOTH browsers

**Expected Console Output (Browser 1 - Room Creator)**:
```
[matrix] ParticipantConnected handler triggered for @guest_xyz789:hush-local.com
[livekit] E2EE key sent to @guest_xyz789:hush-local.com
```

**Expected Console Output (Browser 2 - Late Joiner)**:
```
[livekit] Received E2EE key for different room, ignoring
OR
[livekit] E2EE key received from Matrix to-device message
[livekit] E2EE key applied to existing room
```

**Verification**:
- ✅ Browser 1 logs "E2EE key sent to" with Browser 2's user ID
- ✅ Browser 2 logs "E2EE key received from Matrix to-device message"
- ✅ Browser 2 logs "E2EE key applied to existing room"

**Network Verification (Browser 1)**:
1. DevTools → Network tab
2. Filter for `sendToDevice`
3. Find `/_matrix/client/v3/sendToDevice/m.room_key/...`
4. Inspect request payload

**Expected Payload**:
```json
{
  "messages": {
    "@guest_xyz789:hush-local.com": {
      "KLMNOPQRST": {
        "room_name": "test-e2ee-media",
        "e2ee_key": "abc123def456..."
      }
    }
  }
}
```

**Common Issues**:
- If key not sent, verify Browser 1 is room creator (created the room first)
- If Browser 2 doesn't receive key, check Matrix to-device message delivery
- Verify both browsers are in same Matrix room (check room IDs match)

---

### Test 2.3: Late Joiner Receives Encrypted Media

**Objective**: Verify late joiner can decrypt screen share using received E2EE key.

**Steps**:
1. Browser 1: Screen share active in "test-e2ee-media"
2. Browser 2: After joining room, observe main area for screen shares
3. Browser 2: Click on available screen share card to watch

**Expected UI (Browser 2)**:
- ✅ Screen share card appears in main area (labeled "Screen")
- ✅ Click on card reveals "Watch Screen" option
- ✅ After clicking "Watch", video element displays Browser 1's screen
- ✅ Screen content is visible and not scrambled/black

**Expected Console Output (Browser 2)**:
```
[livekit] Using stored E2EE key from sessionStorage
[livekit] E2EE key provider and worker initialized
[livekit] E2EE enabled with Matrix key distribution
```

**SessionStorage Verification (Browser 2)**:
```javascript
// Paste in console
console.log('E2EE key stored:', sessionStorage.getItem('livekit-e2ee-key-test-e2ee-media'))
```

**Expected Output**:
```
E2EE key stored: abc123def456... (same key as Browser 1)
```

**Verification**:
- ✅ Browser 2 used E2EE key from sessionStorage (received via Matrix)
- ✅ Screen share video decrypts successfully (not black screen)
- ✅ Key matches between Browser 1 and Browser 2

**Common Issues**:
- If screen appears black, E2EE key may not have been applied correctly
- Check console for E2EE worker errors
- Verify sessionStorage contains key for correct room name
- If key missing, check Test 2.2 passes (key distribution)

---

### Test 2.4: Multi-User Screen Share with E2EE

**Objective**: Verify multiple screen shares work with E2EE encryption.

**Steps**:
1. Browser 1: Screen share active in "test-e2ee-media"
2. Browser 2: Start screen share in same room
3. Both browsers: Observe available screen shares

**Expected UI**:
- ✅ Browser 1 sees 2 screen share cards (own + Browser 2's)
- ✅ Browser 2 sees 2 screen share cards (own + Browser 1's)
- ✅ Both can watch each other's screen shares successfully

**Expected Console Output (Browser 2 when starting share)**:
```
[livekit] Using stored E2EE key from sessionStorage
[livekit] E2EE key provider and worker initialized
```

**Verification**:
- ✅ Both screen shares encrypted with same E2EE key
- ✅ Both users can decrypt both streams
- ✅ No key conflicts or decryption errors

**Common Issues**:
- If one screen appears black, check E2EE key consistency across browsers
- Verify both browsers are using same room name (keys are room-scoped)

---

### Test 2.5: E2EE Key Persistence Across Page Reload

**Objective**: Verify E2EE key persists in sessionStorage across page reload.

**Steps**:
1. Browser 2: In room "test-e2ee-media", verify E2EE key received (Test 2.2)
2. Browser 2: Reload page (F5 or Cmd+R)
3. Browser 2: Login as guest and rejoin "test-e2ee-media"
4. Browser 2: Check console output

**Expected Console Output**:
```
[livekit] Using stored E2EE key from sessionStorage
[livekit] E2EE key provider and worker initialized
```

**SessionStorage Verification**:
```javascript
// Paste in console before and after reload
console.log('E2EE key:', sessionStorage.getItem('livekit-e2ee-key-test-e2ee-media'))
```

**Expected Result**:
- ✅ Key exists in sessionStorage before reload
- ✅ Key still exists after reload (same value)
- ✅ Browser 2 doesn't request key from Browser 1 again

**Verification**:
- ✅ "Using stored E2EE key from sessionStorage" message appears
- ✅ No "Received E2EE key from Matrix to-device message" (not re-fetching)
- ✅ Screen shares still decrypt after reload

**Common Issues**:
- If key missing after reload, sessionStorage may have been cleared
- Verify browser settings don't clear sessionStorage on reload
- In incognito mode, sessionStorage persists within same tab but not across new tabs

---

### Test 2.6: E2EE Key Isolation Between Rooms

**Objective**: Verify E2EE keys are room-scoped (different rooms use different keys).

**Steps**:
1. Browser 1: Create and join room "test-room-a"
2. Browser 1: Start screen share, observe console for key generation
3. Browser 1: Note E2EE key from console or sessionStorage
4. Browser 1: Leave room, create and join room "test-room-b"
5. Browser 1: Start screen share, observe console for key generation

**Expected Console Output (Room A)**:
```
[livekit] Generated new random E2EE key (room creator)
```

**Expected Console Output (Room B)**:
```
[livekit] Generated new random E2EE key (room creator)
```

**SessionStorage Verification**:
```javascript
// Paste in console
console.log({
  keyA: sessionStorage.getItem('livekit-e2ee-key-test-room-a'),
  keyB: sessionStorage.getItem('livekit-e2ee-key-test-room-b')
})
```

**Expected Result**:
```javascript
{
  keyA: "abc123...",  // Key for room A
  keyB: "xyz789..."   // Key for room B (different from keyA)
}
```

**Verification**:
- ✅ Two different keys generated (one per room)
- ✅ Keys stored with room-scoped sessionStorage keys
- ✅ Each room uses its own unique E2EE key

**Common Issues**:
- If keys are identical, check room name is being used in sessionStorage key
- Verify key generation uses crypto.getRandomValues() for randomness

---

## Part 3: Browser DevTools Inspection

### 3.1: IndexedDB Inspection

**Check Matrix Crypto Store**:

1. DevTools → Application tab → IndexedDB
2. Expand `hush-crypto` database
3. Click on `account` object store

**Expected Data**:
- One entry with account data (device keys, cross-signing keys)

**Check Olm Sessions**:
1. Click on `sessions` object store
2. Expand entries

**Expected Data**:
- Olm session data for device-to-device communication
- Used for sharing Megolm room keys

**Check Megolm Sessions**:
1. Click on `inbound_group_sessions` object store
2. Expand entries

**Expected Data**:
- Group session keys for each encrypted room
- Format: `{room_id}|{sender_key}|{session_id}`

---

### 3.2: SessionStorage Inspection

**Check LiveKit E2EE Keys**:

1. DevTools → Application tab → Session Storage
2. Expand `http://localhost:5173`
3. Look for keys starting with `livekit-e2ee-key-`

**Expected Keys**:
```
livekit-e2ee-key-test-e2ee-media = "abc123def456..." (base64)
livekit-e2ee-key-test-room-a = "xyz789..." (base64)
```

**Verification**:
- ✅ One key per room
- ✅ Keys are base64-encoded strings
- ✅ Keys persist within same browser session (tab)

---

### 3.3: Network Traffic Inspection

**Matrix API Calls**:

1. DevTools → Network tab
2. Filter: `/_matrix/`
3. Look for these endpoints:

**Expected Requests**:
```
POST /_matrix/client/v3/register?kind=guest
  → Guest registration

POST /_matrix/client/v3/sync
  → Sync loop (repeated requests)

POST /_matrix/client/v3/createRoom
  → Room creation

POST /_matrix/client/v3/rooms/{roomId}/send/m.room.message/{txnId}
  → Send message (payload is ENCRYPTED)

POST /_matrix/client/v3/sendToDevice/m.room_key/{txnId}
  → E2EE key distribution for LiveKit
```

**Verify Proxy Routing**:
- ✅ All `/_matrix/` requests go to `http://localhost:5173` (proxied)
- ✅ Vite dev server forwards to `http://localhost:8008` (Caddy)
- ✅ Caddy forwards to `http://synapse:8008` (Synapse container)

**Check Request Payload Encryption**:
1. Click on `send/m.room.message/` request
2. Click "Payload" tab

**Expected Encrypted Payload**:
```json
{
  "algorithm": "m.megolm.v1.aes-sha2",
  "ciphertext": "AwgAEn...",
  "device_id": "ABCDEFGHIJ",
  "sender_key": "curve25519:...",
  "session_id": "..."
}
```

**NOT Expected (Unencrypted)**:
```json
{
  "msgtype": "m.text",
  "body": "Hello world"  // ❌ Should NOT see plaintext
}
```

---

### 3.4: Console Output Summary

**Successful E2EE Flow Console Output**:

```
[matrix] loginAsGuest: start
[matrix] initCrypto: starting crypto initialization
[matrix] initCrypto: crypto initialized successfully
[matrix] loginAsGuest: startClient called
[matrix] loginAsGuest: sync completed

[home] Crypto status before room operations: { cryptoEnabled: true, ... }
[home] createRoom response: { room_id: "!xyz:..." }

[chat] Component mounted with crypto status: { cryptoEnabled: true, ... }
[chat] Timeline event received: { type: "m.room.message", ... }

[livekit] Generated new random E2EE key (room creator)
[livekit] E2EE key provider and worker initialized
[livekit] E2EE enabled with Matrix key distribution
[livekit] room.e2eeManager: E2EEManager { ... }

[livekit] E2EE key sent to @guest_xyz:hush-local.com
[livekit] E2EE key received from Matrix to-device message
[livekit] E2EE key applied to existing room
```

**Red Flags (Error Indicators)**:
```
❌ initCrypto: ERROR initializing crypto
❌ room configured for encryption but client does not support
❌ Unable to decrypt message
❌ Failed to initialize E2EE key provider
❌ E2EE worker failed to load
```

---

## Part 4: Troubleshooting

### Issue 4.1: Crypto Initialization Fails

**Symptoms**:
- Console shows `initCrypto: ERROR initializing crypto`
- `cryptoEnabled: false` in status logs

**Causes**:
1. IndexedDB disabled in browser settings
2. Browser storage quota exceeded
3. IndexedDB access blocked (Safari private mode)

**Solutions**:
1. Check browser settings → Privacy → Enable IndexedDB
2. Clear site data: DevTools → Application → Clear storage
3. Use normal browser mode (not private/incognito) for testing
4. Check browser console for specific IndexedDB errors

---

### Issue 4.2: Messages Show "Unable to Decrypt"

**Symptoms**:
- Messages appear as "Unable to decrypt" in chat
- Console shows encrypted events but no decrypted events

**Causes**:
1. Crypto not initialized before joining room
2. Megolm session keys not shared between devices
3. IndexedDB crypto store corrupted or cleared

**Solutions**:
1. Verify `cryptoEnabled: true` before sending/receiving messages (Test 1.1, 1.3, 1.4)
2. Check Network tab for `sendToDevice/m.room_key` requests (key sharing)
3. Clear IndexedDB and re-login to reset crypto store
4. Check both users have crypto initialized (both show `cryptoEnabled: true`)

---

### Issue 4.3: LiveKit E2EE Key Not Received

**Symptoms**:
- Late joiner doesn't receive E2EE key
- Console doesn't show "E2EE key received from Matrix"
- Screen share appears as black screen

**Causes**:
1. Room creator didn't detect ParticipantConnected event
2. Matrix to-device message delivery failed
3. Room name mismatch (key scoped to wrong room)

**Solutions**:
1. Verify room creator sees "E2EE key sent to {userId}" in console
2. Check Network tab for `sendToDevice` request with E2EE key payload
3. Verify both browsers joined same Matrix room (check room IDs match)
4. Check sessionStorage for `livekit-e2ee-key-{roomName}` key
5. Reload late joiner browser and rejoin room to trigger key request again

---

### Issue 4.4: Screen Share Appears Black

**Symptoms**:
- Screen share video element shows black screen
- No video content visible
- Audio may or may not work

**Causes**:
1. E2EE key not applied correctly (encryption/decryption mismatch)
2. E2EE worker failed to initialize
3. WebRTC connection issues (unrelated to E2EE)

**Solutions**:
1. Check console for "E2EE key provider and worker initialized" message
2. Verify E2EE key in sessionStorage matches between browsers
3. Check console for E2EE worker errors or WebRTC ICE failures
4. Test without E2EE first to isolate encryption vs connection issues
5. Verify HTTPS/localhost (E2EE worker requires secure context)

---

### Issue 4.5: E2EE Key Missing After Page Reload

**Symptoms**:
- SessionStorage doesn't contain E2EE key after reload
- Late joiner requests key again from room creator

**Causes**:
1. SessionStorage cleared on reload (browser setting)
2. Incognito mode opened new tab (sessionStorage is tab-scoped)
3. Room name changed between sessions

**Solutions**:
1. Check browser settings → Privacy → Don't clear sessionStorage on reload
2. Use same tab for reload testing (don't open new tab in incognito)
3. Verify room name is consistent (E2EE keys are room-scoped)
4. Expected behavior: sessionStorage persists within same tab/session only

---

### Issue 4.6: Synapse Encryption Disabled

**Symptoms**:
- Matrix messages not encrypted
- Network payload shows plaintext `body` field
- No `algorithm` or `ciphertext` in message payload

**Causes**:
1. Synapse `encryption_enabled_by_default_for_room_type` set to `off`
2. Room created with explicit `encryption: false` parameter
3. E2EE disabled at server level

**Solutions**:
1. Check `synapse/data/homeserver.yaml`:
   ```yaml
   encryption_enabled_by_default_for_room_type: all
   ```
2. Restart Synapse container: `docker-compose restart synapse`
3. Create new room (existing rooms retain old encryption settings)
4. Verify `synapse/homeserver.yaml.template` also has encryption enabled

---

### Issue 4.7: Stale Encrypted Rooms in Database

**Symptoms**:
- Room creation fails with alias collision
- Error: `room configured for encryption but client does not support`
- Message sending fails immediately after room creation

**Causes**:
1. Previous encrypted rooms exist in Synapse database with same alias
2. Database not cleared between E2EE testing iterations
3. Room alias reused after disabling/re-enabling encryption

**Solutions**:
1. Use unique room names for each test run
2. App implements automatic collision handling with random suffix
3. Nuclear option: Wipe Synapse database and restart:
   ```bash
   docker-compose down
   docker volume rm hush-app_synapse-data hush-app_postgres-data
   docker-compose up -d
   bash scripts/generate-synapse-config.sh
   docker-compose restart synapse
   ```

---

## Part 5: Success Criteria Summary

### Matrix E2EE ✅
- [ ] Crypto initializes on guest login (Test 1.1)
- [ ] IndexedDB `hush-crypto` database created (Test 1.2)
- [ ] Crypto status logged before room operations (Test 1.3)
- [ ] Chat component logs crypto status on mount (Test 1.4)
- [ ] Messages auto-encrypt when sent (Test 1.5)
- [ ] Multi-user message decryption works (Test 1.6)
- [ ] Messages decrypt after page reload (Test 1.7)

### LiveKit E2EE ✅
- [ ] Room creator generates random E2EE key (Test 2.1)
- [ ] E2EE key distributed to late joiners via Matrix (Test 2.2)
- [ ] Late joiner decrypts screen share successfully (Test 2.3)
- [ ] Multi-user screen shares work with E2EE (Test 2.4)
- [ ] E2EE key persists across page reload (Test 2.5)
- [ ] E2EE keys are room-scoped (different per room) (Test 2.6)

### DevTools Verification ✅
- [ ] IndexedDB contains crypto store data (Part 3.1)
- [ ] SessionStorage contains LiveKit E2EE keys (Part 3.2)
- [ ] Network traffic shows encrypted payloads (Part 3.3)
- [ ] Console output matches expected flow (Part 3.4)

---

## Notes

- **Test Order**: Run Matrix tests (Part 1) before LiveKit tests (Part 2)
- **Clean State**: Clear browser storage between test runs for consistent results
- **Multi-User**: Tests 1.6, 2.2, 2.3, 2.4 require two browsers simultaneously
- **Persistence**: Tests 1.7, 2.5 verify data survives page reload
- **Network Inspection**: Use DevTools Network tab to verify encryption at protocol level
- **Console Logging**: All diagnostic logs prefixed with `[matrix]`, `[chat]`, `[home]`, or `[livekit]`

## Reference

- Matrix E2EE: Olm (device-to-device) + Megolm (group sessions)
- LiveKit E2EE: AES-GCM encryption with shared symmetric key
- Key Distribution: Matrix to-device messages (`m.room_key` event type)
- Crypto Store: IndexedDB `hush-crypto` database (persistent)
- E2EE Keys: SessionStorage `livekit-e2ee-key-{roomName}` (session-scoped)
