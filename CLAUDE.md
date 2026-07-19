# `face-search-service/` — Reglas del agente

> Lee `README.md` (descripción, setup, endpoints, deploy).
> Este documento define **cómo debe comportarse un agente** al trabajar aquí.

## 1. Rol en el polyrepo

Servicio **stateless** Go que recibe una imagen, busca en Rekognition, enrich con datos de Postgres, y devuelve matches con presigned S3 URLs.

**Único consumidor**: el panel admin (`app-socios-estadio-admin`) — llama directo, bypass Rails.

**No guarda estado**: solo lee de PostgreSQL (del backend Rails) para enrich. Zero DB propia.

## 2. Stack y comandos

```bash
go version   # 1.24 requerido (aws-sdk-go-v2 requiere go ≥ 1.24)

go run ./cmd/server          # Dev (requiere DATABASE_URL, FACE_SEARCH_TOKEN, AWS creds)
go build ./cmd/server        # Build binary
docker build -t face-search .  # Build Docker
docker compose up face-search  # Via backend docker-compose
```

## 3. Endpoints

| Método | Path | Auth | Descripción |
|---|---|---|---|
| GET | `/health` | none | `{"status":"ok"}` |
| POST | `/search-face` | Bearer token | Busca cara en Rekognition, enrich con Postgres, retorna matches |

### `POST /search-face`

```json
// Request
{ "image": "data:image/jpeg;base64,<base64>" }

// Response 200
{
  "matches": [
    {
      "user_id": "51",
      "rut": "111111111",
      "phone": "+56912345678",
      "confidence": 99.9999,
      "face_id": "3ea27730-...",
      "photo_url": "https://perfilamiento-faces.s3.../...?X-Amz-Signature=..."
    }
  ],
  "query_time_ms": 520
}

// Errores
400 → sin cara / formato inválido
401 → token faltante o inválido
413 → imagen > 5MB
500 → Rekognition error
503 → rate limit AWS
```

## 4. Flujo interno

```
POST /search-face
    ↓
validateImage() — parse base64, check format/size
    ↓
rekognition.SearchFacesByImage() — busca en collection (threshold 96%, top 10)
    ↓
db.EnrichMatches() — SELECT users, face_records WHERE face_id IN (...) → rut, phone, photo_url
    ↓
s3.Presign() — genera presigned URLs (1h expiry) para cada photo_url
    ↓
Response{matches, query_time_ms}
```

## 5. Estructura de archivos

```
cmd/server/main.go              ← Bootstrap: AWS SDK, DB pool, handlers, CORS
internal/
├── config/config.go            ← os.Getenv() para cada var
├── db/client.go                 ← database/sql queries (users + face_records)
├── handlers/
│   ├── health.go               ← GET /health
│   └── search.go               ← POST /search-face + error mapping
├── middleware/cors.go           ← CORS allowlist from CORS_ORIGINS env
└── rekognition/client.go        ← SearchFacesByImage wrapper + threshold
```

## 6. Variables de entorno

| Var | Requerida | Default | Notes |
|---|---|---|---|
| `PORT` | no | `8080` | HTTP server port |
| `DATABASE_URL` | **sí** | — | Postgres del backend Rails. Sin esto → crash al boot. |
| `FACE_SEARCH_TOKEN` | **sí** | — | Bearer token compartido con admin panel. Sin esto → 401 en todos los requests. |
| `CORS_ORIGINS` | **sí** | — | CSV de origins. Ej: `http://localhost:5174,http://localhost:5175` |
| `AWS_REGION` | sí | `us-east-1` | Región AWS |
| `AWS_ACCESS_KEY_ID` | dev | — | Prod: IAM role del Cloud Run service account |
| `AWS_SECRET_ACCESS_KEY` | dev | — | Prod: IAM role |
| `AWS_S3_BUCKET_NAME` | no | `perfilamiento-faces` | Debe matchear con backend |
| `REKOGNITION_COLLECTION_ID` | no | `socios_stadium_users` | Debe matchear con backend |

## 7. Convenciones de código

- **Go 1.24**: usar generics donde aplique, `errors.Is` / `errors.As` para error handling.
- **Sin ORM**: `database/sql` + `lib/pq` directo. Queries mínimas (2-3 por request).
- **AWS SDK Go v2** (no v1).
- **CORS allowlist explícita**: no `*` (browser bloquea presigned S3 URLs sin CORS headers correctos).
- **Error mapping por `smithy.APIError.ErrorCode()`**: más robusto que `errors.As`.
- **Presigned URL expiry: 1h**. No cacheable más allá de eso.

## 8. Decisiones arquitectónicas cerradas

- **Stateless**: escala horizontal sin coordinación. Cloud Run lo maneja.
- **Enrich desde Postgres compartido**: no crea tabla ni esquema propio.
- **Search threshold 96%**: evitar falsos positivos. Ajustable si hay demasiados falsos negativos.
- **`MaxFaces: 10`**: top 10 matches por query.
- **`QualityFilter: AUTO`**: descarta fotos borrosas en IndexFaces (backend Rails lo usa; no afecta SearchFaces).
- **`ExternalImageId = user.id` (string)**: face IDs son globales en la collection, join por `face_records.user_id`.

## 9. Boundaries

**Hace este servicio:**
- Búsqueda facial en Rekognition (stateless, escala horizontal)
- Enrich de matches con Postgres (rut, phone, photo_url)
- Generación de presigned S3 URLs (1h)

**NO hace este servicio:**
- Face indexing (lo hace el backend Rails via `FaceIndexer`)
- Face deletion (lo hace el backend Rails)
- Auth de admins (cookie JWT httpOnly) — eso es Rails
- Frontend (React SPA) — eso es `app-socios-estadio-admin`
- Camera streaming — eso es `camera-server`

## 10. Gotchas críticas

- **`FROM scratch` en Dockerfile**: sin shell, sin `env`, sin `ls`. Debug: `docker run --rm face-search cat /etc/ssl/certs/ca-certificates.crt | head`.
- **`DATABASE_URL` es requerido**: el servicio crashea al boot si no está. En Cloud Run se monta desde Secret Manager.
- **`FACE_SEARCH_TOKEN` es requerido**: todos los requests sin token válido → 401.
- **Rekognition face IDs son globales**: si se borra un `FaceRecord` en Postgres pero no el face en Rekognition, queda huérfano (orphan face). La búsqueda devuelve el face ID sin enrich de user.
- **Si un user tiene 2 faces indexadas**: el Go service consolida por `user_id` y devuelve la de mayor confidence.
- **CORS**: si `CORS_ORIGINS` no incluye el origin del admin, el browser bloquea la response y la búsqueda falla con error de red.

## 11. Deploy

```bash
# GCP Cloud Build → Cloud Run
# Secrets: DATABASE_URL, FACE_SEARCH_TOKEN (ambos desde GCP Secret Manager)
# Non-secrets: AWS_REGION, REKOGNITION_COLLECTION_ID, CORS_ORIGINS (set via --set-env-vars)
# Prod: IAM role del Cloud Run service account (Workload Identity Federation, NO env vars AWS)
```

## 12. Checklist pre-commit

- [ ] `go build ./...` exit 0
- [ ] `curl http://localhost:8081/health` → 200
- [ ] Smoke test con imagen válida → 200 + matches array
- [ ] Smoke test sin token → 401
- [ ] Smoke test con imagen > 5MB → 413
- [ ] `REKOGNITION_COLLECTION_ID` y `AWS_S3_BUCKET_NAME` matchean con backend Rails
- [ ] `FACE_SEARCH_TOKEN` es el mismo valor exacto en `face-search/.env.development` y `admin/.env.development`

## 13. Tier de riesgo

**External-read + write (medio)** — lee Rekognition y Postgres, genera presigned S3 URLs. Stateless = recovery trivial.

- Cambios a threshold o `MaxFaces`: impacta la UX de face-search directamente (más/menos resultados)
- Cambios a `CORS_ORIGINS`: puede romper el admin panel
- Cambios a `DATABASE_URL` schema: rompe el enrich de matches
- Cambios a collection ID: rompe todas las búsquedas (no encuentra nada)
