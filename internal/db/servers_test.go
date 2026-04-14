package db

import (
	"context"
	"testing"

	"github.com/hushhq/hush-server/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestListServersForUser_NoGuilds_Integration verifies an empty list is returned
// when the user belongs to no guilds. Guards against query-level regressions
// (e.g. type-mismatch errors) even in the zero-row case.
func TestListServersForUser_NoGuilds_Integration(t *testing.T) {
	pool, cleanup := SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	_, err := pool.Exec(ctx, `TRUNCATE messages, channels, server_members, dm_pairs, servers, sessions, users RESTART IDENTITY CASCADE`)
	require.NoError(t, err)

	userID := newTestUser(t, pool, ctx, "Solo User")

	guilds, err := pool.ListServersForUser(ctx, userID)
	require.NoError(t, err)
	assert.Empty(t, guilds)
}

// TestListServersForUser_RegularGuild_Integration verifies a regular (non-DM) guild
// is returned without OtherUser or ChannelID — those are DM-only fields.
func TestListServersForUser_RegularGuild_Integration(t *testing.T) {
	pool, cleanup := SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	_, err := pool.Exec(ctx, `TRUNCATE messages, channels, server_members, dm_pairs, servers, sessions, users RESTART IDENTITY CASCADE`)
	require.NoError(t, err)

	userID := newTestUser(t, pool, ctx, "Guild Owner")
	srv, err := pool.CreateServer(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, pool.AddServerMember(ctx, srv.ID, userID, models.PermissionLevelOwner))

	guilds, err := pool.ListServersForUser(ctx, userID)
	require.NoError(t, err)
	require.Len(t, guilds, 1)

	g := guilds[0]
	assert.Equal(t, srv.ID, g.ID)
	assert.False(t, g.IsDm)
	assert.Nil(t, g.OtherUser, "regular guild must not carry OtherUser")
	assert.Nil(t, g.ChannelID, "regular guild must not carry ChannelID")
}

// TestListServersForUser_DMGuild_Integration is the regression test for the
// uuid/text type mismatch (SQLSTATE 42883) that occurred when user IDs were
// compared against uuid columns inside a CASE expression without an explicit cast.
// It also verifies the DM-enrichment fields (OtherUser, ChannelID) are populated.
func TestListServersForUser_DMGuild_Integration(t *testing.T) {
	pool, cleanup := SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	_, err := pool.Exec(ctx, `TRUNCATE messages, channels, server_members, dm_pairs, servers, sessions, users RESTART IDENTITY CASCADE`)
	require.NoError(t, err)

	callerID := newTestUser(t, pool, ctx, "Caller")
	peerID := newTestUser(t, pool, ctx, "Peer")

	dmSrv, dmCh, err := pool.CreateDMGuild(ctx, callerID, peerID)
	require.NoError(t, err)
	require.NotNil(t, dmSrv)
	require.NotNil(t, dmCh)

	guilds, err := pool.ListServersForUser(ctx, callerID)
	require.NoError(t, err)
	require.Len(t, guilds, 1)

	g := guilds[0]
	assert.Equal(t, dmSrv.ID, g.ID)
	assert.True(t, g.IsDm)

	// OtherUser must be the peer, not the caller.
	require.NotNil(t, g.OtherUser, "DM guild must have OtherUser populated")
	assert.Equal(t, peerID, g.OtherUser.ID)
	assert.NotEmpty(t, g.OtherUser.Username)

	// ChannelID must point to the DM channel.
	require.NotNil(t, g.ChannelID, "DM guild must have ChannelID populated")
	assert.Equal(t, dmCh.ID, *g.ChannelID)
}

// TestListServersForUser_MixedGuilds_Integration verifies that when a user belongs
// to both a regular guild and a DM guild, DM enrichment applies only to the DM guild.
func TestListServersForUser_MixedGuilds_Integration(t *testing.T) {
	pool, cleanup := SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	_, err := pool.Exec(ctx, `TRUNCATE messages, channels, server_members, dm_pairs, servers, sessions, users RESTART IDENTITY CASCADE`)
	require.NoError(t, err)

	callerID := newTestUser(t, pool, ctx, "Mixed Caller")
	peerID := newTestUser(t, pool, ctx, "Mixed Peer")

	srv, err := pool.CreateServer(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, pool.AddServerMember(ctx, srv.ID, callerID, models.PermissionLevelOwner))

	dmSrv, dmCh, err := pool.CreateDMGuild(ctx, callerID, peerID)
	require.NoError(t, err)

	guilds, err := pool.ListServersForUser(ctx, callerID)
	require.NoError(t, err)
	require.Len(t, guilds, 2)

	byID := make(map[string]models.Server, 2)
	for _, g := range guilds {
		byID[g.ID] = g
	}

	regular := byID[srv.ID]
	assert.False(t, regular.IsDm)
	assert.Nil(t, regular.OtherUser)
	assert.Nil(t, regular.ChannelID)

	dm := byID[dmSrv.ID]
	assert.True(t, dm.IsDm)
	require.NotNil(t, dm.OtherUser)
	assert.Equal(t, peerID, dm.OtherUser.ID)
	require.NotNil(t, dm.ChannelID)
	assert.Equal(t, dmCh.ID, *dm.ChannelID)
}
