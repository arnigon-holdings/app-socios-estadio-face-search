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
	UserID     string  `json:"user_id"`
	RUT        string  `json:"rut"`
	Phone      string  `json:"phone"`
	Confidence float64 `json:"confidence"`
	FaceID     string  `json:"face_id,omitempty"`
	PhotoURL   string  `json:"photo_url,omitempty"`
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

	faceMatches, err := h.rekognition.SearchFaces(ctx, imageBytes, 96)
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
	userMatches := make(map[string]*MatchResult)

	for _, match := range faceMatches {
		existing, ok := userMatches[match.UserID]
		if !ok {
			userInfo, err := h.db.GetUserByID(ctx, match.UserID)
			if err != nil || userInfo == nil {
				continue
			}

			photoURL := h.buildPhotoURL(ctx, match.UserID)

			userMatches[match.UserID] = &MatchResult{
				UserID:     match.UserID,
				RUT:        userInfo.RUT,
				Phone:      userInfo.Phone,
				Confidence: match.Confidence,
				FaceID:     match.FaceID,
				PhotoURL:   photoURL,
			}
		} else {
			if match.Confidence > existing.Confidence {
				existing.Confidence = match.Confidence
				existing.FaceID = match.FaceID
			}
		}
	}

	result := make([]MatchResult, 0, len(userMatches))
	for _, match := range userMatches {
		result = append(result, *match)
	}

	return result
}

func (h *SearchHandler) buildPhotoURL(ctx context.Context, userID string) string {
	if h.s3Presigner == nil {
		return ""
	}

	ref, err := h.db.GetLatestFaceRecordByUserID(ctx, userID)
	if err != nil || ref == nil {
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
