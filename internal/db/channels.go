package db

import (
	"context"
	"errors"
	"fmt"

	"hush.app/server/internal/models"

	"github.com/jackc/pgx/v5"
)

// CreateChannel inserts a channel and returns the created row.
func (p *Pool) CreateChannel(ctx context.Context, serverID, name, channelType string, voiceMode *string, parentID *string, position int) (*models.Channel, error) {
	row := p.QueryRow(ctx, `
		INSERT INTO channels (server_id, name, type, voice_mode, parent_id, position)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, server_id, name, type, voice_mode, parent_id, position`,
		serverID, name, channelType, voiceMode, parentID, position,
	)
	return scanChannel(row)
}

// ListChannels returns channels for the server ordered by position, then name.
func (p *Pool) ListChannels(ctx context.Context, serverID string) ([]models.Channel, error) {
	rows, err := p.Query(ctx, `
		SELECT id, server_id, name, type, voice_mode, parent_id, position
		FROM channels WHERE server_id = $1
		ORDER BY position, name`, serverID)
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

// GetChannelByID returns the channel by ID, or nil if not found.
func (p *Pool) GetChannelByID(ctx context.Context, channelID string) (*models.Channel, error) {
	row := p.QueryRow(ctx, `
		SELECT id, server_id, name, type, voice_mode, parent_id, position
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

// DeleteChannel deletes the channel by ID.
func (p *Pool) DeleteChannel(ctx context.Context, channelID string) error {
	_, err := p.Exec(ctx, `DELETE FROM channels WHERE id = $1`, channelID)
	return err
}

// MoveChannel updates a channel's parent and position, shifting sibling channels to
// maintain a contiguous, collision-free position sequence within each scope.
//
// Scope rules (position is ordinal within the scope, 0-indexed):
//   - type='category'                   → among all categories
//   - type!='category', parentID=nil    → among uncategorized non-category channels
//   - type!='category', parentID=some   → among channels within that parent category
func (p *Pool) MoveChannel(ctx context.Context, channelID string, newParentID *string, newPosition int) error {
	tx, err := p.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var oldParentID *string
	var channelType string
	var oldPosition int
	if err := tx.QueryRow(ctx,
		`SELECT parent_id, type, position FROM channels WHERE id = $1`,
		channelID,
	).Scan(&oldParentID, &channelType, &oldPosition); err != nil {
		return fmt.Errorf("get channel state: %w", err)
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

// GetServerIDForChannel returns the server_id for the channel, or empty string if not found.
func (p *Pool) GetServerIDForChannel(ctx context.Context, channelID string) (string, error) {
	var serverID string
	err := p.QueryRow(ctx, `SELECT server_id FROM channels WHERE id = $1`, channelID).Scan(&serverID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return serverID, nil
}

func scanChannel(row pgx.Row) (*models.Channel, error) {
	var c models.Channel
	err := row.Scan(&c.ID, &c.ServerID, &c.Name, &c.Type, &c.VoiceMode, &c.ParentID, &c.Position)
	if err != nil {
		return nil, err
	}
	return &c, nil
}
