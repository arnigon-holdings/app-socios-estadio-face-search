# `face-search-service/` — Go 1.24

Servicio stateless que recibe una imagen, la busca contra la colección de Rekognition, y devuelve los matches con `rut`, `phone` y `photo_url` (presigned S3 1h).

Sin estado. Escala horizontal (Cloud Run target). Sin DB propia — solo lee de PostgreSQL (DB del backend Rails) para enrich los matches.

## Quickstart

```bash
# Solo el servicio (asume DB y Rekognition ya configurados)
DATABASE_URL=postgres://app_perfil:dev_password@localhost:5432/app_perfil_development?sslmode=disable \
  FACE_SEARCH_TOKEN=dev-face-search-token \
  AWS_REGION=us-east-1 \
  AWS_ACCESS_KEY_ID=... \
  AWS_SECRET_ACCESS_KEY=... \
  REKOGNITION_COLLECTION_ID=socios_stadium_users \
  go run ./cmd/server

# O vía docker-compose (recomendado en dev)
cd backend && docker compose up -d face-search
```

Health: `GET /health` → `{"status":"ok"}`. Search: `POST /search-face` con Bearer token.

## Estructura

```
face-search-service/
├── cmd/server/main.go              ← bootstrap: AWS SDK clients + DB + handlers + CORS
├── internal/
│   ├── config/config.go            ← env loader
│   ├── db/client.go                 ← PostgreSQL queries (users + face_records)
│   ├── handlers/
│   │   ├── health.go               ← GET /health
│   │   └── search.go               ← POST /search-face (búsqueda + presigned + error mapping)
│   ├── middleware/cors.go           ← allowlist via CORS_ORIGINS env
│   └── rekognition/client.go        ← SearchFacesByImage wrapper
├── Dockerfile                       ← golang:1.24-alpine + ca-certificates
├── cloudbuild.yaml                  ← GCP Cloud Build para deploy
└── go.mod / go.sum
```

## Endpoints

### `GET /health`

```bash
curl http://localhost:8081/health
# {"status":"ok"}
```

### `POST /search-face`

Auth: `Authorization: Bearer ${FACE_SEARCH_TOKEN}`.

```bash
curl -X POST http://localhost:8081/search-face \
  -H "Origin: http://localhost:5174" \
  -H "Authorization: Bearer dev-face-search-token" \
  -H "Content-Type: application/json" \
  -d '{"image":"data:image/jpeg;base64,..."}'
```

Response:

```json
{
  "matches": [
    {
      "user_id": "51",
      "rut": "111111111",
      "phone": "+56912345678",
      "confidence": 99.9999,
      "face_id": "3ea27730-...",
      "photo_url": "https://perfilamiento-faces.s3.us-east-1.amazonaws.com/...?X-Amz-Signature=..."
    }
  ],
  "query_time_ms": 520
}
```

Errores típicos (HTTP status + mensaje user-friendly):

| Código | Causa | Mensaje |
|---|---|---|
| 400 | Sin cara detectada | "No se detectó un rostro válido en la imagen..." |
| 400 | Formato no soportado | "Formato de imagen no soportado por Rekognition..." |
| 401 | Sin token / token inválido | "missing/invalid authorization header" |
| 413 | Imagen > 5MB | "Imagen demasiado grande para Rekognition (máx 5MB)" |
| 500 | Colección no existe | "Colección de Rekognition no encontrada..." |
| 502 | Error de transporte AWS | "upstream AWS error: no se pudo contactar Rekognition" |
| 503 | Rate limit AWS | "Rekognition rate-limit alcanzado..." |

## Env vars

| Var | Requerida | Default | Descripción |
|---|---|---|---|
| `PORT` | no | `8080` | |
| `DATABASE_URL` | sí | — | `postgres://...?sslmode=disable` |
| `FACE_SEARCH_TOKEN` | sí | — | Bearer token compartido con admin |
| `AWS_REGION` | sí | `us-east-1` | |
| `AWS_ACCESS_KEY_ID` | sí (dev) | — | prod usa IAM role |
| `AWS_SECRET_ACCESS_KEY` | sí (dev) | — | prod usa IAM role |
| `AWS_S3_BUCKET_NAME` | no | `perfilamiento-faces` | Default |
| `REKOGNITION_COLLECTION_ID` | no | `socios_stadium_users` | Default |
| `CORS_ORIGINS` | sí | — | CSV. Sin esto = sin CORS = bloqueado por browser |

## Decisiones

- **AWS SDK Go v2** (no v1) — modular, mejor soporte de `PresignClient`, más activo.
- **Dockerfile `golang:1.24-alpine` + `ca-certificates`**: el v1.24 es necesario porque `aws-sdk-go-v2/service/s3 v1.104.0` requiere `go ≥ 1.24`. El paquete `ca-certificates` se copia al `FROM scratch` para que TLS a AWS funcione (bug original: TLS handshake fallaba con "x509: certificate signed by unknown authority").
- **Sin ORM**: `database/sql` + `lib/pq` directo. Queries son 2-3 máximo.
- **CORS allowlist explícita** (no `*`) — el browser bloquea presigned URLs cross-origin sin CORS headers correctos.
- **Error mapping por `smithy.APIError.ErrorCode()`** — más robusto que `errors.As` a tipos específicos (no todas las AWS errors están tipadas).
- **Search threshold 96%** — match estricto. Ajustable si hay muchos falsos negativos.
- **Búsqueda retorna top 10 matches** (`MaxFaces: 10`).
- **Presigned URL expiry: 1h**. Cacheable en frontend hasta refresh manual.

## Tests

No hay tests unitarios todavía (pendiente en CHECKLIST.md).

```bash
# Smoke test local
go build ./...
./server &  # en otro terminal
curl http://localhost:8081/health
curl -X POST http://localhost:8081/search-face \
  -H "Authorization: Bearer dev-face-search-token" \
  -H "Content-Type: application/json" \
  -d '{"image":"data:image/jpeg;base64,<base64>"}'
```

## Deploy

- **`Dockerfile`** listo para `docker build`. Stage único (`FROM scratch` con binary estático CGO_ENABLED=0).
- **`cloudbuild.yaml`** configura GCP Cloud Build para deploy a Cloud Run.
- **Scaling**: stateless, escala horizontal automáticamente en Cloud Run. Concurrencia por request (1 consulta Rekognition ~200-500ms).
- **Secrets en prod**: usar IAM role attached al Cloud Run service account (no env vars). Workload Identity Federation con GCP está soportado via assume_role_policy (Federated: accounts.google.com).

## Gotchas

- **El service es `FROM scratch`** — sin shell, sin `env`, sin `ls`. Para debug usar `docker run --rm backend-face-search cat /etc/ssl/certs/ca-certificates.crt | head` (no `docker exec`).
- **Default `MaxFaces: 10`** — cambiar si se esperan más matches por query.
- **`QualityFilter: AUTO`** descarta fotos borrosas en IndexFaces (no afecta SearchFaces).
- **Rekognition face IDs son globales en la collection**, no scoped por user. Si borras un `face_records` row pero no el face en Rekognition, queda orphan.
- **`ExternalImageId = user.id`** (string) — Rekognition indexa por face ID, joineamos por `face_records.user_id`. Si un user tiene 2 faces indexadas, Go service consolida por user_id y devuelve la de mayor confidence.
