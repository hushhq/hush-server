// Package e2e contains headless two-client delivery tests (HUSHHQ-106). Each
// test drives two real hush-web clients, one per OS process, through one real
// server built by server.BuildServer, and asserts on observable final state, not
// on mocked mechanism.
//
// TestVoiceConvergence_TwoClients_ThroughRealServer (e2e:protocol)
//   Proves: voice MLS epoch + frame-key convergence; bidirectional decrypt;
//           HUSHHQ-104 / 105a / 105b regressions; eviction forward secrecy.
//   Does NOT prove: real LiveKit/WebRTC media, FrameCryptor decryptFailures, UI.
//
// TestChatDelivery_TwoClients_ThroughRealServer (e2e:chat-delivery-headless)
//   Proves: a text message round-trips A->B and B->A as the exact plaintext
//           through the real server (message.send/message.new), real text MLS
//           group, and real encrypt/decrypt; HUSHHQ-105 text identity-encoding
//           parity. Pass condition is the observed plaintext only.
//   Does NOT prove: React rendering, message-list/scroll UI, notifications,
//           browser behaviour (deferred to the Playwright media/UI milestone).
//
// The tests compile only under -tags e2e_test and skip unless TEST_DATABASE_URL
// (or DATABASE_URL) and HUSH_WEB_DIR are set. This file keeps the package
// non-empty in normal builds so `go build ./...` and `go vet ./...` do not error
// on excluded build constraints.
package e2e
