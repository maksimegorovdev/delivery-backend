# syntax=docker/dockerfile:1
# Образ сервиса order. Контекст сборки — корень репозитория:
#   docker build -f services/order/Dockerfile .
#
# Сервис собирается в workspace вместе с platform и proto: модули ссылаются друг на друга без
# replace, вне workspace go пошёл бы за ними в сеть. Workspace собирается здесь только из нужных
# модулей, поэтому правки других сервисов не сбрасывают кэш. Новая внутренняя зависимость
# сервиса требует строк COPY и пути в go work init ниже.
FROM golang:1.27 AS build
WORKDIR /src

# Слой зависимостей: меняется только вместе с go.mod/go.sum, правки .go его не сбрасывают.
COPY go.work.sum ./
COPY platform/go.mod platform/go.sum platform/
COPY proto/go.mod proto/go.sum proto/
COPY services/order/go.mod services/order/go.sum services/order/
RUN go work init ./platform ./proto ./services/order
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY platform/ platform/
COPY proto/ proto/
COPY services/order/ services/order/
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    cd services/order && CGO_ENABLED=0 go build -trimpath -o /out/app ./cmd

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/app /app
ENTRYPOINT ["/app"]
