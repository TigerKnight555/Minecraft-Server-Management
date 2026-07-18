# --- Frontend build ---
FROM node:22-alpine AS frontend
WORKDIR /src/web
COPY web/package.json web/package-lock.json* ./
RUN npm ci || npm install
COPY web/ ./
RUN npm run build

# --- Backend build ---
FROM golang:1.26-alpine AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /src/web/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /msm ./cmd/msm
# leeres /data-Skelett, damit das Named Volume die Ownership des
# nonroot-Users (65532) erbt — sonst gehört es root und SQLite kann
# nicht schreiben ("unable to open database file")
RUN mkdir /data-skel

# --- Runtime ---
# distroless nonroot: no shell, no package manager, uid 65532
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=backend /msm /msm
COPY --from=backend --chown=65532:65532 /data-skel /data
EXPOSE 8080
VOLUME /data
ENTRYPOINT ["/msm"]
