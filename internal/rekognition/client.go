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
	Similarity float64 `json:"similarity"`
}

func NewClient(client *rekognition.Client, collectionID string) *Client {
	return &Client{
		client:       client,
		collectionID: collectionID,
	}
}

func (c *Client) SearchFaces(ctx context.Context, imageBytes []byte, threshold, maxFaces float32) ([]FaceMatch, error) {
	input := &rekognition.SearchFacesByImageInput{
		CollectionId:       aws.String(c.collectionID),
		Image:             &types.Image{Bytes: imageBytes},
		FaceMatchThreshold: aws.Float32(threshold),
		MaxFaces:          aws.Int32(int32(maxFaces)),
		QualityFilter:      types.QualityFilterNone,
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
		similarity := float64(0)
		if match.Similarity != nil {
			similarity = float64(*match.Similarity)
		}
		matches = append(matches, FaceMatch{
			FaceID:     aws.ToString(match.Face.FaceId),
			UserID:     aws.ToString(match.Face.ExternalImageId),
			Similarity: similarity,
		})
	}

	return matches, nil
}
