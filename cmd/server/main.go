package main

import (
	"context"
	"fmt"
	"log"
	"net/http"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	awssdk "github.com/aws/aws-sdk-go-v2/service/rekognition"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/arnigon/face-search-service/internal/config"
	"github.com/arnigon/face-search-service/internal/db"
	"github.com/arnigon/face-search-service/internal/handlers"
	"github.com/arnigon/face-search-service/internal/middleware"
	"github.com/arnigon/face-search-service/internal/rekognition"
)

func main() {
	cfg := config.Load()

	awsCfg, err := awsconfig.LoadDefaultConfig(context.TODO(),
		awsconfig.WithRegion(cfg.AWSRegion),
	)
	if err != nil {
		log.Fatalf("failed to load AWS config: %v", err)
	}

	rekClient := rekognition.NewClient(awssdk.NewFromConfig(awsCfg), cfg.RekognitionCollection)
	s3Client := s3.NewFromConfig(awsCfg)
	s3Presigner := s3.NewPresignClient(s3Client)

	dbClient, err := db.NewClient(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("failed to create database client: %v", err)
	}
	defer dbClient.Close()

	searchHandler := handlers.NewSearchHandler(cfg, rekClient, dbClient, s3Presigner)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", handlers.Health)
	mux.HandleFunc("POST /search-face", searchHandler.SearchFace)

	handler := middleware.CORS(mux)

	addr := fmt.Sprintf(":%s", cfg.Port)
	log.Printf("starting server on %s", addr)
	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
