package db

import (
	"context"
	"errors"

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
