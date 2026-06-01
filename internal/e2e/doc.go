// Package e2e contains the headless two-client voice/MLS convergence tests
// (HUSHHQ-106). The tests drive two real hush-web clients, each in its own OS
// process, through one real server built by server.BuildServer, and assert on
// their converged final state (epoch + frame-key hash + bidirectional decrypt)
// rather than on mocked mechanism. They reproduce the integration seams that
// HUSHHQ-104 (missing voice WS subscription) and HUSHHQ-105 (member-identity
// base64 encoding + post-removal local merge) slipped past.
//
// The tests compile only under -tags e2e_test and skip unless TEST_DATABASE_URL
// (or DATABASE_URL) and HUSH_WEB_DIR are set. This file keeps the package
// non-empty in normal builds so `go build ./...` and `go vet ./...` do not error
// on excluded build constraints.
package e2e
