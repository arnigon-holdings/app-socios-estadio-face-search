package db

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/lib/pq"
)

type Client struct {
	db *sql.DB
}

type UserInfo struct {
	RUT    string
	Phone  string
}

type FaceRecordRef struct {
	S3Bucket string
	S3Key    string
}

func NewClient(databaseURL string) (*Client, error) {
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return &Client{db: db}, nil
}

func (c *Client) Close() error {
	return c.db.Close()
}

func (c *Client) GetUserByID(ctx context.Context, userID string) (*UserInfo, error) {
	query := `SELECT rut, phone FROM users WHERE id = $1 LIMIT 1`

	var user UserInfo
	err := c.db.QueryRowContext(ctx, query, userID).Scan(&user.RUT, &user.Phone)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	return &user, nil
}

func (c *Client) GetLatestFaceRecordByUserID(ctx context.Context, userID string) (*FaceRecordRef, error) {
	query := `SELECT s3_bucket, s3_key FROM face_records WHERE user_id = $1 ORDER BY indexed_at DESC LIMIT 1`

	var ref FaceRecordRef
	err := c.db.QueryRowContext(ctx, query, userID).Scan(&ref.S3Bucket, &ref.S3Key)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get face_record: %w", err)
	}

	return &ref, nil
}
