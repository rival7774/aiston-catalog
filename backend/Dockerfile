FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /catalog .
FROM scratch
COPY --from=build /catalog /catalog
USER 65532:65532
ENTRYPOINT ["/catalog"]
