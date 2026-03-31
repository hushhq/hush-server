package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/hushhq/hush-server/internal/models"
)

// FindDMGuild returns the DM guild for the given user pair, or pgx.ErrNoRows when
// no DM exists. User IDs are canonically ordered (min < max) so the lookup is
// consistent regardless of the call-site parameter order.
func (p *Pool) FindDMGuild(ctx context.Context, userAID, userBID string) (*models.Server, error) {
	lo, hi := canonicalUserOrder(userAID, userBID)
	row := p.QueryRow(ctx, `
		SELECT s.id, s.encrypted_metadata, s.member_count, s.text_channel_count, s.voice_channel_count,
		       s.storage_bytes, s.message_count, s.active_members_30d, s.last_active_at,
		       s.access_policy, s.discoverable, s.admin_label_encrypted, s.created_at,
		       s.is_dm, s.category, s.public_name, s.public_description
		FROM servers s
		JOIN dm_pairs dp ON dp.server_id = s.id
		WHERE dp.user_a_id = $1 AND dp.user_b_id = $2`,
		lo, hi,
	)
	s, err := scanServer(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, pgx.ErrNoRows
		}
		return nil, err
	}
	return s, nil
}

// CreateDMGuild creates a DM guild for the two users in a single transaction.
// It inserts the servers row (is_dm=true), a dm_pairs entry, both participants
// as PermissionLevelOwner server_members, and one text channel (position 0).
// Returns the created server and the created channel.
func (p *Pool) CreateDMGuild(ctx context.Context, userAID, userBID string) (*models.Server, *models.Channel, error) {
	lo, hi := canonicalUserOrder(userAID, userBID)

	tx, err := p.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// 1. Create the DM server row.
	serverRow := tx.QueryRow(ctx, `
		INSERT INTO servers (is_dm, access_policy)
		VALUES (true, 'closed')
		RETURNING id, encrypted_metadata, member_count, text_channel_count, voice_channel_count,
		          storage_bytes, message_count, active_members_30d, last_active_at,
		          access_policy, discoverable, admin_label_encrypted, created_at,
		          is_dm, category, public_name, public_description`,
	)
	server, err := scanServer(serverRow)
	if err != nil {
		return nil, nil, fmt.Errorf("create dm server: %w", err)
	}

	// 2. Insert dm_pairs entry with canonical user ordering.
	if _, err := tx.Exec(ctx, `
		INSERT INTO dm_pairs (server_id, user_a_id, user_b_id)
		VALUES ($1, $2, $3)`,
		server.ID, lo, hi,
	); err != nil {
		return nil, nil, fmt.Errorf("insert dm_pairs: %w", err)
	}

	// 3. Add both participants as PermissionLevelOwner.
	for _, uid := range []string{userAID, userBID} {
		if _, err := tx.Exec(ctx, `
			INSERT INTO server_members (server_id, user_id, permission_level)
			VALUES ($1, $2, $3)`,
			server.ID, uid, models.PermissionLevelOwner,
		); err != nil {
			return nil, nil, fmt.Errorf("add server member %s: %w", uid, err)
		}
	}

	// 4. Create a single text channel at position 0.
	var ch models.Channel
	channelRow := tx.QueryRow(ctx, `
		INSERT INTO channels (server_id, type, position)
		VALUES ($1, 'text', 0)
		RETURNING id, server_id, encrypted_metadata, type, voice_mode, parent_id, position`,
		server.ID,
	)
	if err := channelRow.Scan(
		&ch.ID, &ch.ServerID, &ch.EncryptedMetadata, &ch.Type, &ch.VoiceMode, &ch.ParentID, &ch.Position,
	); err != nil {
		return nil, nil, fmt.Errorf("create dm channel: %w", err)
	}

	// 5. Update member_count and text_channel_count to reflect reality.
	if _, err := tx.Exec(ctx, `
		UPDATE servers
		SET member_count = 2, text_channel_count = 1
		WHERE id = $1`,
		server.ID,
	); err != nil {
		return nil, nil, fmt.Errorf("update guild counts: %w", err)
	}
	server.MemberCount = 2
	server.TextChannelCount = 1

	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return server, &ch, nil
}

// DiscoverGuilds returns publicly discoverable guilds that are not closed,
// filtered by optional category and search query, sorted by members or creation date.
// Returns the guilds for the requested page plus the total matching count.
// instance_config.guild_discovery = 'disabled' causes an empty result set.
func (p *Pool) DiscoverGuilds(ctx context.Context, category, search, sort string, page, pageSize int) ([]models.DiscoverGuild, int, error) {
	if page < 1 {
		page = 1
	}
	offset := (page - 1) * pageSize

	// Build WHERE clause dynamically.
	args := []interface{}{}
	argIdx := 1

	whereClause := `
		s.discoverable = true
		AND s.access_policy != 'closed'
		AND s.is_dm = false
		AND (
			SELECT COALESCE(ic.guild_discovery, 'enabled')
			FROM instance_config ic
			LIMIT 1
		) != 'disabled'`

	if category != "" {
		args = append(args, category)
		whereClause += fmt.Sprintf(" AND s.category = $%d", argIdx)
		argIdx++
	}
	if search != "" {
		args = append(args, "%"+search+"%")
		whereClause += fmt.Sprintf(" AND s.public_name ILIKE $%d", argIdx)
		argIdx++
	}

	orderClause := "s.member_count DESC, s.created_at DESC"
	if sort == "newest" {
		orderClause = "s.created_at DESC, s.member_count DESC"
	}

	args = append(args, pageSize, offset)
	limitArg := argIdx
	offsetArg := argIdx + 1

	query := fmt.Sprintf(`
		SELECT
			s.id,
			COALESCE(s.public_name, ''),
			COALESCE(s.public_description, ''),
			COALESCE(s.category, ''),
			s.access_policy,
			s.member_count,
			s.created_at,
			COUNT(*) OVER () AS total_count
		FROM servers s
		WHERE %s
		ORDER BY %s
		LIMIT $%d OFFSET $%d`,
		whereClause, orderClause, limitArg, offsetArg,
	)

	rows, err := p.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var guilds []models.DiscoverGuild
	var total int
	for rows.Next() {
		var g models.DiscoverGuild
		if err := rows.Scan(
			&g.ID, &g.PublicName, &g.PublicDescription,
			&g.Category, &g.AccessPolicy, &g.MemberCount, &g.CreatedAt,
			&total,
		); err != nil {
			return nil, 0, err
		}
		guilds = append(guilds, g)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if guilds == nil {
		guilds = []models.DiscoverGuild{}
	}
	return guilds, total, nil
}

// SearchUsersPublic returns users matching the query string on username or displayName.
// Only id, username, and displayName are returned — no ban status, roles, or credentials.
func (p *Pool) SearchUsersPublic(ctx context.Context, query string, limit int) ([]models.UserSearchPublicResult, error) {
	pattern := "%" + query + "%"
	rows, err := p.Query(ctx, `
		SELECT id, username, display_name
		FROM users
		WHERE username ILIKE $1 OR display_name ILIKE $1
		ORDER BY username
		LIMIT $2`,
		pattern, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.UserSearchPublicResult
	for rows.Next() {
		var u models.UserSearchPublicResult
		if err := rows.Scan(&u.ID, &u.Username, &u.DisplayName); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if out == nil {
		out = []models.UserSearchPublicResult{}
	}
	return out, nil
}

// canonicalUserOrder returns the two user IDs in lexicographic ascending order.
// This ensures dm_pairs always stores user_a_id < user_b_id regardless of call order.
func canonicalUserOrder(userAID, userBID string) (lo, hi string) {
	if userAID < userBID {
		return userAID, userBID
	}
	return userBID, userAID
}
