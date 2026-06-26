package rekognition

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rekognition"
	"github.com/aws/aws-sdk-go-v2/service/rekognition/types"
)

type Client struct {
	client       *rekognition.Client
	collectionID string
}

type FaceMatch struct {
	UserID     string  `json:"user_id"`
	FaceID     string  `json:"face_id"`
	Confidence float64 `json:"confidence"`
}

func NewClient(client *rekognition.Client, collectionID string) *Client {
	return &Client{
		client:       client,
		collectionID: collectionID,
	}
}

func (c *Client) SearchFaces(ctx context.Context, imageBytes []byte, threshold float32) ([]FaceMatch, error) {
	input := &rekognition.SearchFacesByImageInput{
		CollectionId:       aws.String(c.collectionID),
		Image:             &types.Image{Bytes: imageBytes},
		FaceMatchThreshold: aws.Float32(threshold),
		MaxFaces:          aws.Int32(10),
	}

	result, err := c.client.SearchFacesByImage(ctx, input)
	if err != nil {
		return nil, err
	}

	matches := make([]FaceMatch, 0, len(result.FaceMatches))
	for _, match := range result.FaceMatches {
		if match.Face == nil {
			continue
		}
		confidence := float64(0)
		if match.Face.Confidence != nil {
			confidence = float64(*match.Face.Confidence)
		}
		matches = append(matches, FaceMatch{
			FaceID:     aws.ToString(match.Face.FaceId),
			UserID:     aws.ToString(match.Face.ExternalImageId),
			Confidence: confidence,
		})
	}

	return matches, nil
}
