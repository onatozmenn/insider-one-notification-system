# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS build

WORKDIR /src

RUN apk add --no-cache ca-certificates git

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

RUN set -eux; \
    for cmd in api worker migrate; do \
        CGO_ENABLED=0 go build -trimpath \
            -ldflags "-s -w \
                -X github.com/onatozmenn/insider-one-notification-system/internal/platform/version.Version=${VERSION} \
                -X github.com/onatozmenn/insider-one-notification-system/internal/platform/version.Commit=${COMMIT} \
                -X github.com/onatozmenn/insider-one-notification-system/internal/platform/version.Date=${BUILD_DATE}" \
            -o "/out/${cmd}" "./cmd/${cmd}"; \
    done

FROM alpine:3.21

RUN addgroup -S app && adduser -S -G app app && apk add --no-cache ca-certificates

WORKDIR /app

COPY --from=build /out/api /app/api
COPY --from=build /out/worker /app/worker
COPY --from=build /out/migrate /app/migrate

USER app

EXPOSE 8080

CMD ["/app/api"]
