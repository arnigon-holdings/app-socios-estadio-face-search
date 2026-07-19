package db

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/lib/pq"
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

func (c *Client) GetUsersAndFaceRecordsByIDs(ctx context.Context, userIDs []string) (map[string]*UserInfo, map[string][]*FaceRecordRef, error) {
	if len(userIDs) == 0 {
		return make(map[string]*UserInfo), make(map[string][]*FaceRecordRef), nil
	}

	query := `
		SELECT u.id::text, u.rut, u.phone,
			fr.s3_bucket, fr.s3_key
		FROM unnest($1::bigint[]) WITH ORDINALITY AS t(user_id, ord)
		JOIN users u ON u.id = t.user_id
		LEFT JOIN LATERAL (
			SELECT s3_bucket, s3_key
			FROM face_records
			WHERE user_id = t.user_id AND indexed_at IS NOT NULL
			ORDER BY indexed_at DESC
		) fr ON true
		ORDER BY t.ord`

	rows, err := c.db.QueryContext(ctx, query, pq.Array(userIDs))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to batch query users and face records: %w", err)
	}
	defer rows.Close()

	users := make(map[string]*UserInfo)
	faceRecords := make(map[string][]*FaceRecordRef)

	for rows.Next() {
		var id, rut, phone string
		var s3Bucket, s3Key sql.NullString
		if err := rows.Scan(&id, &rut, &phone, &s3Bucket, &s3Key); err != nil {
			return nil, nil, fmt.Errorf("failed to scan row: %w", err)
		}
		users[id] = &UserInfo{RUT: rut, Phone: phone}
		if s3Bucket.Valid && s3Key.Valid {
			faceRecords[id] = append(faceRecords[id], &FaceRecordRef{S3Bucket: s3Bucket.String, S3Key: s3Key.String})
		}
	}

	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("rows iteration error: %w", err)
	}

	return users, faceRecords, nil
}
