# ask-form-relay — the public relay. Static binary on scratch, non-root.
FROM golang:1.24-alpine@sha256:8bee1901f1e530bfb4a7850aa7a479d17ae3a18beb6e09064ed54cfd245b7191 AS build
WORKDIR /src
COPY go.mod assets.go install.sh ./
COPY cmd ./cmd
COPY internal ./internal
COPY web ./web
COPY landing ./landing
COPY examples ./examples
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X github.com/GabrielVidal1/ask-form/internal/server.Version=${VERSION}" \
      -o /out/ask-form-relay ./cmd/ask-form-relay

FROM scratch
COPY --from=build /out/ask-form-relay /ask-form-relay
USER 65532:65532
ENV ASK_FORM_RELAY_MACHINES=/data/machines.json
EXPOSE 8080 8081
HEALTHCHECK --interval=30s --timeout=5s CMD ["/ask-form-relay", "healthcheck"]
ENTRYPOINT ["/ask-form-relay"]
CMD ["serve", "--listen", ":8080", "--machine-listen", ":8081"]
