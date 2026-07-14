FROM node:24-alpine AS web
WORKDIR /src/web
COPY web/package*.json ./
RUN npm ci --no-audit --no-fund
COPY web ./
RUN npm run build \
    && if [ -d out ]; then cp -a out /tmp/autostream-control-panel-web; \
       elif [ -d dist ]; then cp -a dist /tmp/autostream-control-panel-web; \
       else echo "control-panel web build did not produce web/out or web/dist" >&2; exit 1; fi

FROM golang:1.26.5-trixie AS build
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN go build -o /out/control-panel -ldflags="-s -w -X github.com/example/autostream-control-panel/internal/version.Version=${VERSION} -X github.com/example/autostream-control-panel/internal/version.Commit=${COMMIT} -X github.com/example/autostream-control-panel/internal/version.BuildDate=${BUILD_DATE}" ./cmd/control-panel

FROM gcr.io/distroless/base-debian13
COPY --from=build /out/control-panel /usr/local/bin/control-panel
COPY --from=web /tmp/autostream-control-panel-web /usr/share/autostream-control-panel
ENV AUTOSTREAM_WEB_DIR=/usr/share/autostream-control-panel
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/control-panel"]
