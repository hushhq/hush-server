# Attachment Security Model

This document describes how Hush handles encrypted chat attachments when the server uses an S3-compatible attachment backend such as MinIO, S3, or R2.

## Simple Model

Hush encrypts chats and attachments differently because they have different sizes and delivery paths.

A normal chat message is small. The client puts the message in a JSON envelope, encrypts that envelope with MLS, and sends the resulting ciphertext to the server. The server stores and routes the ciphertext, but it cannot read the message.

An attachment is larger. The client does not put the whole file inside MLS. Instead, the client encrypts the file locally with a fresh one-time AES key, uploads only the encrypted file bytes to storage, and then sends a small MLS-encrypted message that contains the attachment reference and the AES key needed to decrypt it.

The practical result is:

| Data | Encryption path | Server can read it? |
|-|-|-|
| Text message | Directly encrypted by MLS | No |
| Attachment file bytes | Encrypted locally with AES-GCM before upload | No |
| Attachment AES key and IV | Stored inside the MLS-encrypted message envelope | No |
| Attachment row metadata | Stored by the server for authorization and routing | Yes |

Object storage is therefore a dumb encrypted-byte store. MinIO or S3 can be public-facing behind presigned URLs without becoming trusted for content confidentiality.

## Current Cryptographic Format

Each attachment upload uses one independent key:

| Field | Current value |
|-|-|
| File cipher | AES-GCM |
| Key size | 256 bits |
| IV size | 96 bits |
| Key source | Fresh `crypto.subtle.generateKey()` per attachment |
| IV source | Fresh `crypto.getRandomValues()` per attachment |
| Integrity | AES-GCM authentication tag verified on decrypt |
| MLS exporter use | Not used for attachment keys |

The client stores the attachment decrypt parameters in `AttachmentRef`:

| Field | Meaning | Server visibility |
|-|-|-|
| `id` | Attachment row id minted by the server | Visible |
| `name` | Original display filename | Hidden inside MLS ciphertext |
| `size` | Ciphertext byte size | Visible in attachment row and hidden copy in envelope |
| `mimeType` | Declared MIME type | Visible in attachment row and hidden copy in envelope |
| `key` | Base64 raw AES-256 key | Hidden inside MLS ciphertext |
| `iv` | Base64 96-bit AES-GCM IV | Hidden inside MLS ciphertext |

The visible MIME type is advisory. The server cannot verify plaintext file type because it only sees ciphertext.

## Upload Flow

1. The client rejects files over the attachment limit or outside the MIME allowlist.
2. The client encrypts the file with a fresh AES-GCM-256 key and IV.
3. The client computes the ciphertext size. AES-GCM adds a 16-byte authentication tag, so server-side limits are applied to ciphertext size.
4. The client calls:

   ```text
   POST /api/servers/{serverId}/channels/{channelId}/attachments/presign
   ```

5. The server verifies authentication, channel membership, size, and content type.
6. The server creates an `attachments` row with channel id, owner id, storage key, ciphertext size, content type, creation time, and optional deletion time.
7. The server returns a short-lived presigned upload URL.
8. The client uploads ciphertext bytes directly to object storage.
9. The client sends a normal MLS application message whose plaintext envelope contains the `AttachmentRef`.

If presign generation fails after the database row is created, the server soft-deletes the row as best-effort cleanup.

## Download Flow

1. The recipient receives a chat message as MLS ciphertext.
2. The client decrypts the MLS message and validates the envelope.
3. The client reads each `AttachmentRef` from the envelope.
4. The client calls:

   ```text
   GET /api/attachments/{id}/download
   ```

5. The server checks that the requester is still a member of the attachment's channel.
6. The server returns a short-lived presigned download URL.
7. The client downloads ciphertext from object storage.
8. The client decrypts locally with `AttachmentRef.key` and `AttachmentRef.iv`.
9. If ciphertext, key, or IV do not match, AES-GCM verification fails and the UI must show a failed attachment state.

## What a Server Compromise Exposes

A compromised Hush server or database can see:

- attachment ids
- channel ids
- owner user ids
- storage keys
- ciphertext sizes
- declared content types
- creation and deletion timestamps
- encrypted object bytes if storage is also accessible

It cannot see:

- plaintext attachment bytes
- attachment AES keys
- attachment IVs
- original filenames
- message text surrounding the attachment

Size, timing, channel membership, sender identity, and declared content type remain metadata. Hush is zero-knowledge for attachment content, not metadata-free.

## Forward Secrecy and Revocation

Attachment keys are independent from MLS epoch keys. The key is random per file and remains valid for that encrypted object for as long as the object and the key exist.

Consequences:

- Rotating the MLS epoch does not rotate old attachment keys.
- Removing a member stops them from receiving future MLS messages, but cannot retract attachment keys they already decrypted.
- A member who already has the `AttachmentRef` can decrypt the blob later if they can still fetch or otherwise obtain the ciphertext.
- Local client persistence matters: if a client caches decrypted message envelopes or plaintext transcript rows, endpoint compromise may reveal historical attachment keys.

This is the same control-plane/data-plane tradeoff used by many end-to-end encrypted messengers: MLS protects the small attachment pointer; object storage carries encrypted bulk data.

## Why Hush Does Not Put Files Directly in MLS

The server caps MLS message ciphertext at 8 KiB, and the client caps the plaintext message envelope below that. This keeps WebSocket delivery predictable and avoids using the MLS application-message path for large payloads.

Attachments use out-of-band storage because:

- files are much larger than chat envelopes
- presigned object storage supports direct browser upload/download
- storage can scale independently from chat message routing
- the server still cannot decrypt content

## Why Hush Does Not Derive Attachment Keys From MLS Exporters

Hush currently uses MLS `export_secret` for domains that should follow the current epoch, such as voice frame keys and encrypted metadata keys. Attachments do not use MLS exporters today.

A possible future attachment format could derive an epoch-bound key encryption key from MLS and use it to wrap each random per-file key. That would tighten the forward-secrecy boundary for historical attachment keys. It would also require a versioned `AttachmentRef` format and careful migration behavior for old clients.

The current format intentionally optimizes for simple delivery, independent storage, and compatibility with historical message access.

## Storage Integrity

Attachment integrity is enforced cryptographically by AES-GCM on the client. If object storage returns modified bytes, decryption fails.

The chat attachment flow currently does not include a server-side post-upload checksum confirmation endpoint. The link-device archive flow has separate S3 checksum confirmation logic; that logic should not be assumed for chat attachments.

## Deletion and Orphans

Deleting an attachment soft-deletes the database row and then attempts to delete the storage object. The storage delete is best-effort:

- if the database soft-delete succeeds, the user-facing attachment is gone
- if the backend delete fails, the object may remain in storage
- hidden orphan objects require later operator cleanup or a reconciliation job

Deleting a channel snapshots attachment storage keys before the database cascade and then performs best-effort backend deletion for those objects.

## Operational Requirements

For hosted or domain-mode self-host deployments:

- configure `storage.<DOMAIN>` DNS when using bundled MinIO
- keep attachment storage credentials server-side only
- set bucket CORS to allow the frontend origin to use presigned URLs
- use separate buckets for link-device archives and chat attachments when possible
- treat object-storage logs as metadata-sensitive because paths, sizes, and timing can reveal behavior

Chat attachments require an S3-compatible storage backend. The `postgres_bytea` fallback is used by the link-device archive path for small self-host setups; it is not the chat attachment storage plane.

Relevant environment variables:

| Variable group | Purpose |
|-|-|
| `STORAGE_*` | Default storage backend configuration |
| `ATTACHMENT_STORAGE_*` | Optional attachment-specific storage override |
| `STORAGE_BROWSER_ORIGIN` | Frontend origin allowed by storage CORS |
| `MINIO_ROOT_USER` / `MINIO_ROOT_PASSWORD` | Bundled MinIO credentials in domain mode |

See [RUNBOOK.md](RUNBOOK.md) for storage setup and CORS configuration.
