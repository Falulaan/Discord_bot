FROM golang:1.24.5 AS build

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o /bot ./...

FROM gcr.io/distroless/base-debian12
WORKDIR /app
COPY --from=build /bot /app/bot
ENV PORT=8080
USER nonroot:nonroot
ENTRYPOINT ["/app/bot"]
