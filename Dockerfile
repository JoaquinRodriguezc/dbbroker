FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/dbbroker ./cmd/dbbroker

FROM alpine:3.21
RUN apk add --no-cache ca-certificates postgresql17-client
COPY --from=build /out/dbbroker /usr/local/bin/dbbroker
USER nobody
EXPOSE 8080
ENTRYPOINT ["dbbroker"]
