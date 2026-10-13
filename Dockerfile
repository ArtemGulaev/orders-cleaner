FROM golang:1.23-alpine AS build
WORKDIR /app
COPY go.mod ./
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -o /orders-cleaner .

FROM alpine:3.20
RUN apk --no-cache add ca-certificates
COPY --from=build /orders-cleaner /orders-cleaner
EXPOSE 8080
USER 10001
ENTRYPOINT ["/orders-cleaner"]
