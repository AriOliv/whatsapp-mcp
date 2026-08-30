# Single-binary WhatsApp MCP (whatsmeow, Go). Pure-Go (CGO off) so it runs on
# distroless static.
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/whatsapp-mcp ./cmd/whatsapp-mcp

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/whatsapp-mcp /whatsapp-mcp
ENV MCP_MODE=http PORT=3000
EXPOSE 3000
ENTRYPOINT ["/whatsapp-mcp"]
