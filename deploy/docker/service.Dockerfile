# syntax=docker/dockerfile:1
# Общий образ сервисов. Контекст сборки — корень репозитория:
#   docker build -f deploy/docker/service.Dockerfile --build-arg SERVICE=order .
#
# Сборка идёт в workspace (go.work): модули ссылаются друг на друга без replace
# (notification берёт proto псевдоверсией), вне workspace go пошёл бы за ними в сеть.
# Новый модуль в go.work требует строк COPY его go.mod/go.sum ниже.
FROM golang:1.27 AS build
ARG SERVICE
WORKDIR /src

# Слой зависимостей: меняется только вместе с go.mod/go.sum, правки .go его не сбрасывают.
COPY go.work go.work.sum ./
COPY platform/go.mod platform/go.sum platform/
COPY proto/go.mod proto/go.sum proto/
COPY services/gateway/go.mod services/gateway/go.sum services/gateway/
COPY services/notification/go.mod services/notification/go.sum services/notification/
COPY services/order/go.mod services/order/go.sum services/order/
COPY services/product/go.mod services/product/go.sum services/product/
COPY services/user/go.mod services/user/go.sum services/user/
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    cd services/${SERVICE} && CGO_ENABLED=0 go build -trimpath -o /out/app ./cmd

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/app /app
ENTRYPOINT ["/app"]
