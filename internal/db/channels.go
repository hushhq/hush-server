package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/hushhq/hush-server/internal/models"

	"github.com/jackc/pgx/v5"
)

// CreateChannel inserts a channel scoped to the given guild and returns the created row.
// encryptedMetadata is the client-encrypted channel name/description blob (may be nil for
// system channels, which clients display using the channel type).
func (p *Pool) CreateChannel(ctx context.Context, serverID string, encryptedMetadata []byte, channelType string, parentID *string, position int) (*models.Channel, error) {
	row := p.QueryRow(ctx, `
		INSERT INTO channels (server_id, encrypted_metadata, type, parent_id, position)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, server_id, encrypted_metadata, type, parent_id, position`,
		serverID, encryptedMetadata, channelType, parentID, position,
	)
	return scanChannel(row)
}

// DeleteChannelTree atomically removes a channel and, when the target
// is a category, every channel parented to it. Snapshots the
// attachment storage keys for every removed channel inside the same
// transaction so the caller can schedule async blob cleanup without a
// race against the cascade.
//
// Returns:
//   - deletedIDs: ids actually removed (may be empty if a concurrent
//     admin already deleted the row); the slice always contains the
//     root id last when it was deleted, so callers that rely on
//     stable ordering get a deterministic shape.
//   - storageKeys: every not-yet-tombstoned attachment storage_key
//     across the removed channels. Order is unspecified.
//   - error: pgx.ErrNoRows when the root channel did not exist or
//     belonged to a different server (cross-guild attempt). All other
//     errors are wrapped.
//
// Children must be removed in the same SQL statement because the
// schema declares `parent_id ON DELETE SET NULL`: deleting the
// category first would silently reparent the children to root, which
// is the opposite of what the user-visible "delete category"
// affordance promises.
func (p *Pool) DeleteChannelTree(ctx context.Context, channelID, serverID string) (deletedIDs []string, storageKeys []string, err error) {
	tx, err := p.Begin(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Confirm the root exists in this server before doing anything else
	// so cross-guild attempts surface as 404 (pgx.ErrNoRows) and never
	// leak attachment metadata.
	var rootType string
	if err := tx.QueryRow(ctx,
		`SELECT type FROM channels WHERE id = $1 AND server_id = $2`,
		channelID, serverID,
	).Scan(&rootType); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, pgx.ErrNoRows
		}
		return nil, nil, fmt.Errorf("lookup root: %w", err)
	}

	// Build the target id set. For a category, include every direct
	// child; the schema disallows nested categories at the API layer
	// (createChannel rejects parented categories), so a single
	// parent_id sweep is sufficient.
	targetIDs := []string{channelID}
	if rootType == "category" {
		rows, err := tx.Query(ctx,
			`SELECT id FROM channels WHERE parent_id = $1 AND server_id = $2`,
			channelID, serverID,
		)
		if err != nil {
			return nil, nil, fmt.Errorf("list children: %w", err)
		}
		var children []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, nil, fmt.Errorf("scan child: %w", err)
			}
			children = append(children, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, nil, fmt.Errorf("iterate children: %w", err)
		}
		// Children first, root last — gives callers (logging,
		// broadcasts) a stable order.
		targetIDs = append(children, channelID)
	}

	// Snapshot attachment storage keys before the cascade fires.
	// The explicit `::uuid[]` cast guards against pgx inferring text[]
	// from a Go []string and tripping `operator does not exist:
	// uuid = text` on the channel_id comparison. Same cast on the
	// DELETE below for the same reason.
	keyRows, err := tx.Query(ctx, `
		SELECT storage_key FROM attachments
		WHERE channel_id = ANY($1::uuid[]) AND deleted_at IS NULL`,
		targetIDs,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("list attachment keys: %w", err)
	}
	for keyRows.Next() {
		var key string
		if err := keyRows.Scan(&key); err != nil {
			keyRows.Close()
			return nil, nil, fmt.Errorf("scan attachment key: %w", err)
		}
		storageKeys = append(storageKeys, key)
	}
	keyRows.Close()
	if err := keyRows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate attachment keys: %w", err)
	}

	// Single-statement cascade: every target row goes in one SQL
	// round-trip. RETURNING id reports the rows actually deleted so a
	// race with another admin (row already gone) is handled implicitly
	// — the caller broadcasts only the ids that really vanished.
	delRows, err := tx.Query(ctx, `
		DELETE FROM channels
		WHERE server_id = $1 AND id = ANY($2::uuid[])
		RETURNING id`,
		serverID, targetIDs,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("delete channels: %w", err)
	}
	deletedSet := make(map[string]struct{}, len(targetIDs))
	for delRows.Next() {
		var id string
		if err := delRows.Scan(&id); err != nil {
			delRows.Close()
			return nil, nil, fmt.Errorf("scan deleted id: %w", err)
		}
		deletedSet[id] = struct{}{}
	}
	delRows.Close()
	if err := delRows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate deleted ids: %w", err)
	}

	// Postgres does not guarantee that RETURNING preserves the order
	// of the input array, so reproject the deleted set onto targetIDs
	// to enforce the documented children-first / root-last contract.
	// Callers (logging, broadcast loop) rely on this stable ordering;
	// the underlying SQL wouldn't.
	deletedIDs = make([]string, 0, len(deletedSet))
	for _, id := range targetIDs {
		if _, ok := deletedSet[id]; ok {
			deletedIDs = append(deletedIDs, id)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, nil, fmt.Errorf("commit: %w", err)
	}
	return deletedIDs, storageKeys, nil
}

// ListChannels returns all channels for the given guild ordered by position.
func (p *Pool) ListChannels(ctx context.Context, serverID string) ([]models.Channel, error) {
	rows, err := p.Query(ctx, `
		SELECT id, server_id, encrypted_metadata, type, parent_id, position
		FROM channels
		WHERE server_id = $1
		ORDER BY position`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Channel
	for rows.Next() {
		c, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// GetChannelByTypeAndPosition returns the channel matching both type and position within a guild,
// or nil if not found. Replaces GetChannelByNameAndType for idempotency checks during
// template channel creation (no name column exists after migration 000017).
func (p *Pool) GetChannelByTypeAndPosition(ctx context.Context, serverID, channelType string, position int) (*models.Channel, error) {
	row := p.QueryRow(ctx, `
		SELECT id, server_id, encrypted_metadata, type, parent_id, position
		FROM channels WHERE server_id = $1 AND type = $2 AND position = $3 LIMIT 1`,
		serverID, channelType, position)
	c, err := scanChannel(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return c, nil
}

// GetChannelByID returns the channel by ID, or nil if not found.
func (p *Pool) GetChannelByID(ctx context.Context, channelID string) (*models.Channel, error) {
	row := p.QueryRow(ctx, `
		SELECT id, server_id, encrypted_metadata, type, parent_id, position
		FROM channels WHERE id = $1`, channelID)
	c, err := scanChannel(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return c, nil
}

// DeleteChannel deletes the channel iff (channelID, serverID) matches.
// Returns pgx.ErrNoRows when no row was deleted so handlers can map the
// cross-guild case to 404 without distinguishing it from "not found".
func (p *Pool) DeleteChannel(ctx context.Context, channelID, serverID string) error {
	tag, err := p.Exec(ctx,
		`DELETE FROM channels WHERE id = $1 AND server_id = $2`,
		channelID, serverID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// MoveChannel updates a channel's parent and position, shifting sibling channels to
// maintain a contiguous, collision-free position sequence within each scope.
//
// Scope rules (position is ordinal within the scope, 0-indexed):
//   - type='category'                   → among all categories
//   - type!='category', parentID=nil    → among uncategorized non-category channels
//   - type!='category', parentID=some   → among channels within that parent category
func (p *Pool) MoveChannel(ctx context.Context, channelID, serverID string, newParentID *string, newPosition int) error {
	tx, err := p.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var oldParentID *string
	var channelType string
	var oldPosition int
	// Bind the channel lookup to serverID so a caller that targets a
	// foreign guild's channel sees pgx.ErrNoRows and the handler maps
	// the result to 404. Defence in depth alongside the handler check.
	if err := tx.QueryRow(ctx,
		`SELECT parent_id, type, position FROM channels WHERE id = $1 AND server_id = $2`,
		channelID, serverID,
	).Scan(&oldParentID, &channelType, &oldPosition); err != nil {
		return err
	}

	// If a new parent is requested, it must also belong to serverID.
	// We do not constrain the parent's type here — that gate lives in
	// the handler — but we do refuse cross-guild reparenting.
	if newParentID != nil {
		var parentServerID string
		if err := tx.QueryRow(ctx,
			`SELECT server_id FROM channels WHERE id = $1`,
			*newParentID,
		).Scan(&parentServerID); err != nil {
			return err
		}
		if parentServerID != serverID {
			return pgx.ErrNoRows
		}
	}

	isCategory := channelType == "category"

	// Close gap at old position: shift sibling channels after old position down by 1.
	if err := shiftPositions(ctx, tx, channelID, isCategory, oldParentID, ">", oldPosition, -1); err != nil {
		return fmt.Errorf("close gap: %w", err)
	}

	// Make room at new position: shift sibling channels at new position up by 1.
	if err := shiftPositions(ctx, tx, channelID, isCategory, newParentID, ">=", newPosition, +1); err != nil {
		return fmt.Errorf("make room: %w", err)
	}

	if _, err := tx.Exec(ctx,
		`UPDATE channels SET parent_id = $2, position = $3 WHERE id = $1`,
		channelID, newParentID, newPosition,
	); err != nil {
		return fmt.Errorf("update channel: %w", err)
	}

	return tx.Commit(ctx)
}

// IsChannelMember checks whether the user belongs to the guild that owns the channel.
// A user is a channel member if they are a member of the server that contains the channel.
func (p *Pool) IsChannelMember(ctx context.Context, channelID, userID string) (bool, error) {
	var exists bool
	err := p.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM server_members sm
			JOIN channels c ON c.server_id = sm.server_id
			WHERE c.id = $1 AND sm.user_id = $2
		)`, channelID, userID,
	).Scan(&exists)
	return exists, err
}

// shiftPositions adjusts the position of sibling channels in the given scope.
// op is either ">" (close gap) or ">=" (make room); delta is -1 or +1.
func shiftPositions(ctx context.Context, tx pgx.Tx, channelID string, isCategory bool, parentID *string, op string, pivotPos, delta int) error {
	// Build the scope predicate: categories shift only other categories; regular
	// channels shift only siblings with the same parent_id (null or specific).
	// Using three separate queries avoids dynamic SQL and keeps parameterization safe.
	var err error
	if isCategory {
		_, err = tx.Exec(ctx,
			`UPDATE channels SET position = position + $3
			 WHERE id != $1 AND position `+op+` $2 AND type = 'category'`,
			channelID, pivotPos, delta,
		)
	} else if parentID == nil {
		_, err = tx.Exec(ctx,
			`UPDATE channels SET position = position + $3
			 WHERE id != $1 AND position `+op+` $2
			   AND parent_id IS NULL AND type != 'category'`,
			channelID, pivotPos, delta,
		)
	} else {
		_, err = tx.Exec(ctx,
			`UPDATE channels SET position = position + $3
			 WHERE id != $1 AND position `+op+` $2 AND parent_id = $4`,
			channelID, pivotPos, delta, *parentID,
		)
	}
	return err
}

func scanChannel(row pgx.Row) (*models.Channel, error) {
	var c models.Channel
	err := row.Scan(&c.ID, &c.ServerID, &c.EncryptedMetadata, &c.Type, &c.ParentID, &c.Position)
	if err != nil {
		return nil, err
	}
	return &c, nil
}
