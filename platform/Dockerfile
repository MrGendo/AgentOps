# Go 平台镜像：多阶段构建；入口先执行数据库迁移再启动服务（compose 场景 PG 就绪即迁移）
FROM golang:1.27-alpine AS build
# 受限网络下使用镜像源拉取模块
ENV GOPROXY=https://goproxy.cn,direct
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/server ./cmd/server \
 && CGO_ENABLED=0 go build -o /out/migrate ./cmd/migrate

FROM alpine:3.20
RUN adduser -D app
USER app
COPY --from=build /out/server /usr/local/bin/server
COPY --from=build /out/migrate /usr/local/bin/migrate
COPY --from=build /src/migrations /migrations
ENV AGENTOPS_MIGRATIONS_DIR=/migrations
ENTRYPOINT ["sh", "-c", "migrate -up -dir $AGENTOPS_MIGRATIONS_DIR && exec server"]
EXPOSE 8080
