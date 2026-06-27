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

Toda la configuración se carga desde variables de entorno. **No hay secretos hardcodeados en código** (los defaults en `internal/config/config.go` son solo valores de dev razonables).

### Archivos de configuración

| Archivo | Estado | Propósito |
|---|---|---|
| `face-search-service/.env.example` | tracked | Template con todas las variables documentadas |
| `face-search-service/.env.development` | tracked (sin secretos) | Defaults de dev — funciona con `go run` o via `backend/docker-compose.yml` |
| `face-search-service/.env` | gitignored | Override local |
| `face-search-service/.env.production` | gitignored | Seteado por Secret Manager / Cloud Run en deploy |

### Carga desde código

`internal/config/config.go` carga cada var con `os.Getenv()` y default solo si está vacía. Ver el struct `Config` para el shape completo.

### Tabla de variables

| Var | Requerida | Default | Para qué sirve |
|---|---|---|---|
| `PORT` | no | `8080` | Puerto HTTP del server. Render lo lee como `PORT` también. |
| `DATABASE_URL` | **sí** | — | URL Postgres del backend Rails. Formato: `postgres://user:pass@host:port/db?sslmode=disable`. Sin esto el service crashea al boot. |
| `FACE_SEARCH_TOKEN` | **sí** | — | Bearer token compartido con admin. Sin esto, todos los requests son 401. **PROD: rotar y montar desde Secret Manager**. |
| `CORS_ORIGINS` | **sí** | — | CSV de origins permitidos. Sin esto = sin CORS headers = browser bloquea. Dev: `http://localhost:5174`. |
| `AWS_REGION` | sí | `us-east-1` | Región AWS (Rekognition + S3). |
| `AWS_ACCESS_KEY_ID` | solo dev | — | IAM user con permisos S3 + Rekognition. **PROD: IAM role, no env var**. |
| `AWS_SECRET_ACCESS_KEY` | solo dev | — | Idem. **PROD: IAM role**. |
| `AWS_S3_BUCKET_NAME` | no | `perfilamiento-faces` | Bucket para presigned URLs de fotos de socios (thumbnail en face-search results). |
| `REKOGNITION_COLLECTION_ID` | no | `socios_stadium_users` | ID de la colección Rekognition donde se buscan caras. **Debe matchear con backend** (`backend/.env.development`). |

### Dónde cambiar cada clave (resumen rápido)

- **Cambiar token compartido con admin**: `FACE_SEARCH_TOKEN` en este `.env.development` + `VITE_FACE_SEARCH_TOKEN` en `admin/.env.development`. Mismo valor exacto en ambos.
- **Cambiar DB**: `DATABASE_URL`. Apuntar al Postgres correcto. Dev: misma DB que el backend Rails (`app_perfil_development`).
- **Rotar AWS keys (dev)**: `AWS_ACCESS_KEY_ID` y `AWS_SECRET_ACCESS_KEY`. En prod no se setean (IAM role del Cloud Run service account).
- **Cambiar CORS origins**: `CORS_ORIGINS` (CSV). Ej: `CORS_ORIGINS=https://admin.x.cl,https://admin-staging.x.cl`.
- **Cambiar colección Rekognition**: `REKOGNITION_COLLECTION_ID`. Debe matchear con backend (si no, el face-search no encuentra nada).
- **Cambiar bucket S3**: `AWS_S3_BUCKET_NAME`. Debe matchear con backend (si no, las presigned URLs no funcionan).

### Deploy a GCP Cloud Run

El `cloudbuild.yaml` deploya el container y monta 2 secrets desde GCP Secret Manager:

- `DATABASE_URL` → debe existir como secret `DATABASE_URL` en Secret Manager
- `FACE_SEARCH_TOKEN` → debe existir como secret `FACE_SEARCH_TOKEN`

```bash
# Crear secrets antes del primer deploy
echo -n "postgres://user:pass@/db?host=/cloudsql/instance" | \
  gcloud secrets create DATABASE_URL --data-file=-
echo -n "$(openssl rand -hex 32)" | \
  gcloud secrets create FACE_SEARCH_TOKEN --data-file=-

# Dar acceso al Cloud Run service account
gcloud secrets add-iam-policy-binding DATABASE_URL \
  --member="serviceAccount:PROJECT_ID@appspot.gserviceaccount.com" \
  --role="roles/secretmanager.secretAccessor"
```

Las vars no-secreto (`AWS_REGION`, `REKOGNITION_COLLECTION_ID`) se setean con `--set-env-vars=` en cloudbuild.yaml. Las credenciales AWS reales NO se setean — el Cloud Run service account usa Workload Identity Federation para asumir un IAM role con permisos S3 + Rekognition.

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

No hay tests unitarios todavía (pendiente en [`CHECKLIST.md`](https://github.com/arnigon-holdings/app-socios-estadio-docs/blob/main/CHECKLIST.md)).

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
