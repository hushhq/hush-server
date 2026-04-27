package db

import (
	"context"
	"errors"

	"github.com/hushhq/hush-server/internal/models"
	"github.com/jackc/pgx/v5"
)

// CreateServer inserts a new guild with an optional encrypted metadata blob and returns the created row.
// encryptedMetadata may be nil - the two-step creation flow allows the client to set it after
// establishing the guild metadata MLS group.
func (p *Pool) CreateServer(ctx context.Context, encryptedMetadata []byte) (*models.Server, error) {
	row := p.QueryRow(ctx, `
		INSERT INTO servers (encrypted_metadata)
		VALUES ($1)
		RETURNING id, encrypted_metadata, member_count, text_channel_count, voice_channel_count,
		          storage_bytes, message_count, active_members_30d, last_active_at,
		          access_policy, discoverable, admin_label_encrypted, created_at,
		          member_cap_override, is_dm, category, public_name, public_description`,
		encryptedMetadata,
	)
	return scanServer(row)
}

// UpdateServerEncryptedMetadata replaces the encrypted_metadata blob for the given guild.
// Returns pgx.ErrNoRows if no server with that ID exists (caller maps to 404).
func (p *Pool) UpdateServerEncryptedMetadata(ctx context.Context, serverID string, encryptedMetadata []byte) error {
	tag, err := p.Exec(ctx, `
		UPDATE servers SET encrypted_metadata = $2 WHERE id = $1`,
		serverID, encryptedMetadata,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// GetServerByID returns the guild by ID, or nil if not found.
func (p *Pool) GetServerByID(ctx context.Context, serverID string) (*models.Server, error) {
	row := p.QueryRow(ctx, `
		SELECT id, encrypted_metadata, member_count, text_channel_count, voice_channel_count,
		       storage_bytes, message_count, active_members_30d, last_active_at,
		       access_policy, discoverable, admin_label_encrypted, created_at,
		       member_cap_override, is_dm, category, public_name, public_description
		FROM servers WHERE id = $1`, serverID)
	s, err := scanServer(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return s, nil
}

// ListServersForUser returns all guilds the user is a member of, ordered by creation time.
// DM guilds are enriched with OtherUser and ChannelID via JOINs on dm_pairs, users, and channels.
func (p *Pool) ListServersForUser(ctx context.Context, userID string) ([]models.Server, error) {
	rows, err := p.Query(ctx, `
		SELECT s.id, s.encrypted_metadata, s.member_count, s.text_channel_count, s.voice_channel_count,
		       s.storage_bytes, s.message_count, s.active_members_30d, s.last_active_at,
		       s.access_policy, s.discoverable, s.admin_label_encrypted, s.created_at,
		       s.member_cap_override, s.is_dm, s.category, s.public_name, s.public_description,
		       ou.id, ou.username, ou.display_name,
		       dm_ch.id
		FROM servers s
		JOIN server_members sm ON sm.server_id = s.id AND sm.user_id = $1::uuid
		LEFT JOIN dm_pairs dp ON s.is_dm = true AND dp.server_id = s.id
		LEFT JOIN users ou ON ou.id = (CASE
		    WHEN dp.user_a_id = $1::text THEN dp.user_b_id
		    WHEN dp.user_b_id = $1::text THEN dp.user_a_id
		    ELSE NULL
		END)::uuid
		LEFT JOIN LATERAL (
		    SELECT id FROM channels WHERE server_id = s.id ORDER BY position LIMIT 1
		) dm_ch ON s.is_dm = true
		ORDER BY s.created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Server
	for rows.Next() {
		var s models.Server
		var otherID, otherUsername, otherDisplayName, chID *string
		if err := rows.Scan(
			&s.ID, &s.EncryptedMetadata, &s.MemberCount, &s.TextChannelCount, &s.VoiceChannelCount,
			&s.StorageBytes, &s.MessageCount, &s.ActiveMembers30d, &s.LastActiveAt,
			&s.AccessPolicy, &s.Discoverable, &s.AdminLabelEncrypted, &s.CreatedAt,
			&s.MemberCapOverride, &s.IsDm, &s.Category, &s.PublicName, &s.PublicDescription,
			&otherID, &otherUsername, &otherDisplayName, &chID,
		); err != nil {
			return nil, err
		}
		if otherID != nil {
			s.OtherUser = &models.UserSearchPublicResult{
				ID:          *otherID,
				Username:    derefString(otherUsername),
				DisplayName: derefString(otherDisplayName),
			}
			s.ChannelID = chID
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Enrich DM guilds with unread count for the calling user.
	for i, s := range out {
		if !s.IsDm || s.ChannelID == nil {
			continue
		}
		count, cErr := p.GetUnreadCount(ctx, *s.ChannelID, userID)
		if cErr != nil {
			return nil, cErr
		}
		out[i].Channels = []models.DmChannelSummary{{ID: *s.ChannelID, UnreadCount: count}}
	}
	return out, nil
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// DeleteServer removes the guild by ID.
func (p *Pool) DeleteServer(ctx context.Context, serverID string) error {
	_, err := p.Exec(ctx, `DELETE FROM servers WHERE id = $1`, serverID)
	return err
}

// ListGuildBillingStats returns guild infrastructure metrics for the instance operator.
// No guild name, channel list, or member details are exposed.
//
// member_count is computed LIVE from server_members rather than read from
// the cached `servers.member_count` column. The cached counter is
// incremented only on invite-driven joins (api/servers.go:568,
// api/invites.go:254) and is NOT incremented at server creation for
// the owner, NOR decremented on kick/ban/leave/admin-remove, so it
// drifts arbitrarily — the symptom operators see is "instance admin
// shows servers with 0 members when they actually have members".
// Live counting via LEFT JOIN is authoritative; the cached counter
// stays in the schema for backward compatibility but is no longer
// trusted by this read path.
func (p *Pool) ListGuildBillingStats(ctx context.Context) ([]models.GuildBillingStats, error) {
	rows, err := p.Query(ctx, `
		SELECT s.id,
		       COALESCE(c.cnt, 0) AS member_count,
		       s.storage_bytes, s.message_count, s.active_members_30d,
		       s.last_active_at, s.created_at, s.member_cap_override
		FROM servers s
		LEFT JOIN (
		    SELECT server_id, COUNT(*) AS cnt
		    FROM server_members
		    GROUP BY server_id
		) c ON c.server_id = s.id
		ORDER BY s.created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.GuildBillingStats
	for rows.Next() {
		var g models.GuildBillingStats
		if err := rows.Scan(
			&g.ID, &g.MemberCount, &g.StorageBytes, &g.MessageCount,
			&g.ActiveMembers30d, &g.LastActiveAt, &g.CreatedAt, &g.MemberCapOverride,
		); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// IncrementGuildMessageCount increments the message_count and updates last_active_at for the
// guild that owns the given channel. Called on every relayed message.
func (p *Pool) IncrementGuildMessageCount(ctx context.Context, channelID string) error {
	_, err := p.Exec(ctx, `
		UPDATE servers
		SET message_count = message_count + 1,
		    last_active_at = now()
		WHERE id = (SELECT server_id FROM channels WHERE id = $1)`,
		channelID,
	)
	return err
}

// IncrementGuildMemberCount adjusts member_count by delta (+1 on join, -1 on leave).
func (p *Pool) IncrementGuildMemberCount(ctx context.Context, serverID string, delta int) error {
	_, err := p.Exec(ctx, `
		UPDATE servers SET member_count = member_count + $2 WHERE id = $1`,
		serverID, delta,
	)
	return err
}

// UpdateGuildChannelCounts recalculates text_channel_count and voice_channel_count for the guild.
// Uses a COUNT subquery so the result is always consistent with the channels table.
func (p *Pool) UpdateGuildChannelCounts(ctx context.Context, serverID string) error {
	_, err := p.Exec(ctx, `
		UPDATE servers SET
			text_channel_count  = (SELECT COUNT(*) FROM channels WHERE server_id = $1 AND type = 'text'),
			voice_channel_count = (SELECT COUNT(*) FROM channels WHERE server_id = $1 AND type = 'voice')
		WHERE id = $1`,
		serverID,
	)
	return err
}

// CountOwnedServers returns the number of non-DM servers where the user is the owner.
func (p *Pool) CountOwnedServers(ctx context.Context, userID string) (int, error) {
	var count int
	err := p.QueryRow(ctx, `
		SELECT COUNT(*) FROM server_members sm
		JOIN servers s ON s.id = sm.server_id
		WHERE sm.user_id = $1 AND sm.permission_level = $2 AND s.is_dm = false`,
		userID, models.PermissionLevelOwner,
	).Scan(&count)
	return count, err
}

// UpdateServerMemberCapOverride sets or clears the per-server member cap override.
// Pass nil to clear (inherit instance default).
func (p *Pool) UpdateServerMemberCapOverride(ctx context.Context, serverID string, cap *int) error {
	_, err := p.Exec(ctx, `UPDATE servers SET member_cap_override = $1 WHERE id = $2`, cap, serverID)
	return err
}

func scanServer(row pgx.Row) (*models.Server, error) {
	var s models.Server
	err := row.Scan(
		&s.ID, &s.EncryptedMetadata, &s.MemberCount, &s.TextChannelCount, &s.VoiceChannelCount,
		&s.StorageBytes, &s.MessageCount, &s.ActiveMembers30d, &s.LastActiveAt,
		&s.AccessPolicy, &s.Discoverable, &s.AdminLabelEncrypted, &s.CreatedAt,
		&s.MemberCapOverride, &s.IsDm, &s.Category, &s.PublicName, &s.PublicDescription,
	)
	if err != nil {
		return nil, err
	}
	return &s, nil
}
