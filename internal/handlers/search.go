package handlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/arnigon/face-search-service/internal/config"
	"github.com/arnigon/face-search-service/internal/db"
	"github.com/arnigon/face-search-service/internal/rekognition"
)

type SearchHandler struct {
	cfg         *config.Config
	rekognition *rekognition.Client
	db          *db.Client
	s3Presigner *s3.PresignClient
}

type SearchRequest struct {
	Image string `json:"image"`
}

type SearchResponse struct {
	Matches     []MatchResult `json:"matches"`
	QueryTimeMs int64         `json:"query_time_ms"`
}

type MatchResult struct {
	UserID            string   `json:"user_id"`
	RUT               string   `json:"rut"`
	Phone             string   `json:"phone"`
	Confidence        float64  `json:"confidence"`
	AvgSimilarity     float64  `json:"avg_similarity"`
	FacesCount       int      `json:"faces_count"`
	ConsensusBoosted  bool     `json:"consensus_boosted"`
	PhotoURL         string   `json:"photo_url"`
	FaceID           string   `json:"face_id,omitempty"`
	PhotoURLs        []string `json:"photo_urls"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

func NewSearchHandler(cfg *config.Config, rekClient *rekognition.Client, dbClient *db.Client, s3Presigner *s3.PresignClient) *SearchHandler {
	return &SearchHandler{
		cfg:         cfg,
		rekognition: rekClient,
		db:          dbClient,
		s3Presigner: s3Presigner,
	}
}

func (h *SearchHandler) SearchFace(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	if err := h.validateAuth(r); err != nil {
		h.writeError(w, http.StatusUnauthorized, err.Error())
		return
	}

	var req SearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	imageBytes, err := h.extractImageBytes(req.Image)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if len(imageBytes) == 0 {
		h.writeError(w, http.StatusBadRequest, "empty image payload")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	faceMatches, err := h.rekognition.SearchFaces(ctx, imageBytes, float32(h.cfg.FaceSearchThreshold), float32(h.cfg.MaxFaces))
	if err != nil {
		h.writeRekognitionError(w, err)
		return
	}

	matches := h.consolidateMatches(ctx, faceMatches)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(SearchResponse{
		Matches:     matches,
		QueryTimeMs: time.Since(start).Milliseconds(),
	})
}

func (h *SearchHandler) validateAuth(r *http.Request) error {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return fmt.Errorf("missing authorization header")
	}

	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		return fmt.Errorf("invalid authorization format")
	}

	token := parts[1]
	if token != h.cfg.FaceSearchToken {
		return fmt.Errorf("invalid token")
	}

	return nil
}

func (h *SearchHandler) extractImageBytes(imageData string) ([]byte, error) {
	parts := strings.SplitN(imageData, ",", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid image format, expected data URI")
	}

	return base64.StdEncoding.DecodeString(parts[1])
}

func (h *SearchHandler) consolidateMatches(ctx context.Context, faceMatches []rekognition.FaceMatch) []MatchResult {
	// Group face matches by numeric userID extracted from ExternalImageId.
	type userMatch struct {
		result      MatchResult
		similarities []float64
	}
	userMatches := make(map[string]*userMatch)

	// ExternalImageId is "userID_pose" (e.g. "51_pose_0"). Extract numeric userID.
	uniqueIDs := make([]string, 0, len(faceMatches))
	seen := make(map[string]bool)
	for _, m := range faceMatches {
		if !seen[m.UserID] {
			seen[m.UserID] = true
			uniqueIDs = append(uniqueIDs, h.extractUserID(m.UserID))
		}
	}

	users, faceRecordsMap, err := h.db.GetUsersAndFaceRecordsByIDs(ctx, uniqueIDs)
	if err != nil {
		log.Printf("[face-search] batch user query failed: %v", err)
	}

	for _, match := range faceMatches {
		userID := h.extractUserID(match.UserID)
		um, ok := userMatches[userID]
		if !ok {
			userInfo, ok := users[userID]
			if !ok || userInfo == nil {
				continue
			}

			photoURLs := make([]string, 0)
			var firstPhotoURL string
			if refs, ok := faceRecordsMap[userID]; ok {
				for _, ref := range refs {
					if url := h.buildPhotoURLFromRef(ctx, userID, ref); url != "" {
						photoURLs = append(photoURLs, url)
					}
				}
				if len(photoURLs) > 0 {
					firstPhotoURL = photoURLs[0]
				}
			}

			userMatches[userID] = &userMatch{
				result: MatchResult{
					UserID:     userID,
					RUT:        userInfo.RUT,
					Phone:      userInfo.Phone,
					Confidence: match.Similarity,
					AvgSimilarity: match.Similarity,
					FacesCount: 1,
					PhotoURL:  firstPhotoURL,
					FaceID:   match.FaceID,
					PhotoURLs: photoURLs,
				},
				similarities: []float64{match.Similarity},
			}
		} else {
			um.similarities = append(um.similarities, match.Similarity)
			um.result.FacesCount++
			if match.Similarity > um.result.Confidence {
				um.result.Confidence = match.Similarity
				um.result.FaceID = match.FaceID
			}
		}
	}

	// Apply consensus boost: if >=3 faces with avg >= 85%, boost confidence 5%.
	const consensusMinFaces = 3
	const consensusMinAvg = 85.0
	const consensusBoostPct = 0.05

	result := make([]MatchResult, 0, len(userMatches))
	for _, um := range userMatches {
		n := len(um.similarities)
		sum := 0.0
		for _, s := range um.similarities {
			sum += s
		}
		avg := sum / float64(n)

		um.result.AvgSimilarity = avg

		if n >= consensusMinFaces && avg >= consensusMinAvg {
			um.result.ConsensusBoosted = true
			boosted := um.result.Confidence * (1 + consensusBoostPct)
			if boosted > 100 {
				boosted = 100
			}
			um.result.Confidence = boosted
		}

		result = append(result, um.result)
	}

	return result
}

// extractUserID extracts the numeric user ID from an ExternalImageId
// (e.g. "51_pose_0" -> "51", "123" -> "123").
func (h *SearchHandler) extractUserID(externalImageID string) string {
	if idx := strings.Index(externalImageID, "_"); idx != -1 {
		return externalImageID[:idx]
	}
	return externalImageID
}

func (h *SearchHandler) buildPhotoURL(ctx context.Context, userID string) string {
	if h.s3Presigner == nil {
		return ""
	}

	ref, err := h.db.GetLatestFaceRecordByUserID(ctx, userID)
	if err != nil || ref == nil {
		return ""
	}

	return h.buildPhotoURLFromRef(ctx, userID, ref)
}

func (h *SearchHandler) buildPhotoURLFromRef(ctx context.Context, userID string, ref *db.FaceRecordRef) string {
	if h.s3Presigner == nil || ref == nil {
		return ""
	}

	bucket := ref.S3Bucket
	if bucket == "" {
		bucket = h.cfg.S3Bucket
	}

	presigned, err := h.s3Presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: &bucket,
		Key:    &ref.S3Key,
	}, func(opts *s3.PresignOptions) {
		opts.Expires = 1 * time.Hour
	})
	if err != nil {
		log.Printf("[face-search] presign error user=%s: %v", userID, err)
		return ""
	}

	return presigned.URL
}

func (h *SearchHandler) writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(ErrorResponse{Error: message})
}

func (h *SearchHandler) writeRekognitionError(w http.ResponseWriter, err error) {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		fmt.Printf("[face-search] non-API error: %v\n", err)
		h.writeError(w, http.StatusBadGateway, "upstream AWS error: no se pudo contactar Rekognition")
		return
	}

	switch apiErr.ErrorCode() {
	case "InvalidParameterException":
		h.writeError(w, http.StatusBadRequest,
			"No se detectó un rostro válido en la imagen. Asegúrate de que la foto muestre una sola cara, bien iluminada y de frente.")
	case "InvalidImageFormatException":
		h.writeError(w, http.StatusBadRequest,
			"Formato de imagen no soportado por Rekognition. Usa JPEG o PNG.")
	case "ImageTooLargeException":
		h.writeError(w, http.StatusRequestEntityTooLarge,
			"Imagen demasiado grande para Rekognition (máx 5MB).")
	case "InvalidS3ObjectException":
		h.writeError(w, http.StatusBadRequest,
			"Objeto S3 inválido o inaccesible.")
	case "AccessDeniedException":
		h.writeError(w, http.StatusInternalServerError,
			"Rekognition rechazó la solicitud por permisos. Revisar IAM del Go service.")
	case "ResourceNotFoundException":
		h.writeError(w, http.StatusInternalServerError,
			"Colección de Rekognition no encontrada. Verificar REKOGNITION_COLLECTION_ID.")
	case "ProvisionedThroughputExceededException", "ThrottlingException":
		h.writeError(w, http.StatusServiceUnavailable,
			"Rekognition rate-limit alcanzado. Reintentar en unos segundos.")
	default:
		h.writeError(w, http.StatusInternalServerError, fmt.Sprintf("search failed: %s", apiErr.ErrorCode()))
	}
}
